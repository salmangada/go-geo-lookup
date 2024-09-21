package cachegenerator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"com.sal/geo/utils"
	"github.com/bradfitz/gomemcache/memcache"
	"golang.org/x/time/rate"
)

func ProcessCache(cache *memcache.Client) {

	log.Println("Started Cache Processing")
	// Given the extreme latitude and longitude for India.
	startLat := 7.00
	// endLat := 38.00     ---->  Here for reference
	startLon := 68.70
	endLon := 97.40

	// Convert the above values to int for more precision.
	iStartLat := int(startLat * 100)
	// iEndLat := int(endLat * 100)		---->  Here for reference
	iStartLon := int(startLon * 100)
	iEndLon := int(endLon * 100)

	// We will run iteration for Longitude from start to end for each latitude window in separate goRoutine
	// (10 goroutine in our case) for concurrent processing of cache.
	latStartWindow := iStartLat
	latEndWindow := iStartLat + 310

	// initialize the http client for request to ES, this will be reused in each go-routine as its concurrent safe
	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	limiter := rate.NewLimiter(100, 1)
	maxConcurrentRequests := 5

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentRequests)
	for i := 0; i < 10; i++ {

		wg.Add(1)
		go func(latStartWindow, latEndWindow, iStartLon, iEndLon int, distance string, client *http.Client, cache *memcache.Client, limiter *rate.Limiter) {

			defer wg.Done()

			// Acquire the semaphore (blocks if maxConcurrentRequests is reached)
			sem <- struct{}{}
			defer func() { <-sem }() // Release the semaphore once done

			log.Printf("Goroutine %d has started. Processing latStart: %d, latEnd: %d, lonStart: %d, lonEnd: %d",
				i, latStartWindow, latEndWindow, iStartLon, iEndLon)
			processZipPoint(latStartWindow, latEndWindow, iStartLon, iEndLon, distance, client, cache, limiter)

		}(latStartWindow, latEndWindow, iStartLon, iEndLon, "25km", client, cache, limiter)

		latStartWindow = latEndWindow + 1
		latEndWindow = latEndWindow + 310
	}

	// Wait for all go-routines to complete
	wg.Wait()
	log.Println("Completed the processing of cache")
}

func processZipPoint(latStartWindow, latEndWindow, iStartLon, iEndLon int,
	distance string,
	client *http.Client,
	cache *memcache.Client,
	limiter *rate.Limiter) {

	indexLon := iStartLon
	for latStartWindow <= latEndWindow {
		log.Printf("Starting the window %d", latStartWindow)
		for indexLon <= iEndLon {

			// Respect rate limit before making the request
			if err := limiter.Wait(context.Background()); err != nil {
				log.Printf("Rate limiter error: %v", err)
				continue
			}

			// Retry logic with backoff in case of failure
			retryCount := 0
			var geoPoint *NearestGeoPoint
			var err error

			for retryCount < 3 {
				geoPoint, err = fetchZipPoint(latStartWindow, indexLon, distance, client)
				if err == nil {
					break
				}

				if err.Error() == "no hits found" {
					break
				}

				log.Printf("Failed to fetch for lat: %d, lon: %d - %v. Retrying... (%d/%d)", latStartWindow, indexLon, err, retryCount+1, 3)
				retryCount++
				time.Sleep(time.Duration(retryCount*retryCount) * time.Second)
			}

			if err != nil {
				log.Printf("No Point fetched for lat: %d, lon: %d after %d retries - %v", latStartWindow, indexLon, 3, err)
				indexLon++
				continue
			}

			cacheKey := utils.GenerateUniqueKey(latStartWindow, indexLon)

			geoPointBytes, err := structToBytes(geoPoint)
			if err != nil {
				log.Printf("Error marshalling geoPoint: %v", err)
				indexLon++
				continue
			}

			log.Printf("Fetched Point for lat: %d, lon: %d", latStartWindow, indexLon)
			cache.Set(&memcache.Item{Key: cacheKey, Value: geoPointBytes})
			indexLon++
		}
		indexLon = iStartLon
		latStartWindow++
	}
}

func fetchZipPoint(lat, lon int, distance string, client *http.Client) (*NearestGeoPoint, error) {

	latFloat := float64(lat) / 100
	lonFloat := float64(lon) / 100

	query := map[string]interface{}{
		"size": 1,
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"filter": map[string]interface{}{
					"geo_distance": map[string]interface{}{
						"distance": distance,
						"location": map[string]float64{
							"lat": latFloat,
							"lon": lonFloat,
						},
					},
				},
			},
		},
	}

	// Convert the query to JSON
	jsonData, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	// Create a new HTTP POST request
	req, err := http.NewRequest("POST", os.Getenv("ELASTIC_URL"), bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("error parsing response for %f and %f: %v", latFloat, lonFloat, err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Add basic authentication header
	auth := base64.StdEncoding.EncodeToString([]byte(os.Getenv("ELASTIC_USERNAME") + ":" + os.Getenv("ELASTIC_PASSWORD")))
	req.Header.Set("Authorization", "Basic "+auth)

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error parsing response for %f and %f: %v", latFloat, lonFloat, err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error parsing response for %f and %f: %v", latFloat, lonFloat, err)
	}

	// Unwrap and parse the response into NearestGeoPoint
	geoPoint, err := unWrapResponse(body)
	if err != nil {
		return nil, err
	}

	return geoPoint, nil
}

func unWrapResponse(jsonData []byte) (*NearestGeoPoint, error) {

	var esResponse ESResponse
	err := json.Unmarshal([]byte(jsonData), &esResponse)

	if err != nil {
		return nil, err
	}

	if len(esResponse.Hits.Hits) > 0 {
		geoPoint := esResponse.Hits.Hits[0].Source
		return &geoPoint, nil
	}

	return nil, fmt.Errorf("no hits found")
}

func structToBytes(geoPoint *NearestGeoPoint) ([]byte, error) {
	data, err := json.Marshal(geoPoint)
	if err != nil {
		return nil, err
	}
	return data, nil
}
