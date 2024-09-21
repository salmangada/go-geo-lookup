package main

import (
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"

	"com.sal/geo/cachegenerator"
	"com.sal/geo/utils"
	"github.com/bradfitz/gomemcache/memcache"
	"github.com/joho/godotenv"
)

type App struct {
	cache *memcache.Client
}

// Hello Handler
func getHello(w http.ResponseWriter, r *http.Request) {
	log.Printf("got /hello request\n")
	io.WriteString(w, "Hello, HTTP!\n")
}

// Handler for processing cache -- Time Taking process
func (app *App) processCache(w http.ResponseWriter, r *http.Request) {
	log.Printf("got /process-cache request\n")
	cachegenerator.ProcessCache(app.cache)
}

// Fetch Location Handler
func (app *App) fetchLocation(w http.ResponseWriter, r *http.Request) {
	log.Printf("got /fetch-location request\n")
	latitudeStr := r.URL.Query().Get("latitude")
	longitudeStr := r.URL.Query().Get("longitude")

	if latitudeStr == "" || longitudeStr == "" {
		http.Error(w, "Please provide both latitude and longitude as query parameters", http.StatusBadRequest)
		return
	}

	latitude64, err := strconv.ParseFloat(latitudeStr, 32)
	if err != nil {
		http.Error(w, "Invalid latitude value", http.StatusBadRequest)
		return
	}

	longitude64, err := strconv.ParseFloat(longitudeStr, 32)
	if err != nil {
		http.Error(w, "Invalid longitude value", http.StatusBadRequest)
		return
	}

	latitude := int(math.Round(latitude64 * 100))
	longitude := int(math.Round(longitude64 * 100))

	log.Printf("Fetching the cache for Lat %d and Long %d", latitude, longitude)
	key := utils.GenerateUniqueKey(latitude, longitude)
	item, err := app.cache.Get(key)

	if err == memcache.ErrCacheMiss {
		responseBody := "No nearest Zip found for provided latitude and longitude"
		w.Write([]byte(responseBody))
		return
	} else if err != nil {
		http.Error(w, "Error fetching from cache", http.StatusInternalServerError)
		return
	}

	responseBody := string(item.Value)
	w.Write([]byte(responseBody))

}

func main() {

	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	app := &App{
		cache: memcache.New("127.0.0.1:11211"),
	}

	http.HandleFunc("/", getHello)
	http.HandleFunc("/process-cache", app.processCache)
	http.HandleFunc("/fetch-location", app.fetchLocation)

	err = http.ListenAndServe(":3333", nil)
	if errors.Is(err, http.ErrServerClosed) {
		log.Printf("server closed")
	} else if err != nil {
		log.Printf("error starting server: %s", err)
		os.Exit(1)
	}

}
