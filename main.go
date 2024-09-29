package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

type Item struct {
	Title   string `xml:"title"`
	PubDate string `xml:"pubDate"`
}

var (
	results   = make(chan Item, 100) // Buffered channel to avoid blocking
	itemMutex sync.Mutex
	wg        sync.WaitGroup
	items     []Item // Global variable to store items
)

func init() {
	file, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		logrus.Fatalf("Failed to open log file: %v", err)
	}
	logrus.SetOutput(file)
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "DEBUG" {
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}
}

func main() {
	rssURLs := []string{
		"https://feeds.bbci.co.uk/news/rss.xml",
		"https://news.yahoo.com/rss/",
		"https://www1.cbn.com/rss-cbn-articles-cbnnews.xml",
	}

	// Signal handling for graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Fetch RSS feeds concurrently
	for _, url := range rssURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			logrus.WithField("url", u).Debug("Starting fetch for RSS feed")
			if err := fetchRSS(u); err != nil {
				logrus.WithFields(logrus.Fields{"url": u, "error": err}).Error("Error processing RSS feed")
			}
			logrus.WithField("url", u).Debug("Completed fetch for RSS feed")
		}(url)
	}

	// Collect parsed results
	go func() {
		wg.Wait()
		logrus.Info("All RSS feeds processed. Closing results channel.")
		close(results)
	}()

	go func() {
		for item := range results {
			itemMutex.Lock()
			logrus.WithField("item", item).Debug("Adding item to results")
			items = append(items, item)
			itemMutex.Unlock()
		}
		logrus.Debug("Completed processing results channel")
	}()

	http.HandleFunc("/rss_summary", serveHTML)
	logrus.Info("Serving on http://localhost:8080/rss_summary")

	// Start HTTP server in a Goroutine
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			logrus.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-sigs
	logrus.Info("Received shutdown signal, exiting gracefully...")
	wg.Wait()      // Wait for all Goroutines to finish
	close(results) // Close results channel to clean up
}

// fetchRSS fetches the RSS feed from the specified URL and processes the items.
func fetchRSS(url string) error {
	for i := 0; i < 3; i++ {
		logrus.WithField("url", url).Debugf("Attempt %d to fetch RSS feed", i+1)
		resp, err := http.Get(url)
		if err != nil {
			logrus.WithField("url", url).WithError(err).Error("Failed to fetch RSS feed, retrying...")
			time.Sleep(time.Duration(2<<i) * time.Second)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logrus.WithField("url", url).Errorf("Received non-200 response: %s", resp.Status)
			return fmt.Errorf("received non-200 response: %s", resp.Status)
		}

		if err := parseRSS(resp.Body); err != nil {
			logrus.WithField("url", url).WithError(err).Error("Error parsing RSS feed")
			return err
		}

		logrus.WithField("url", url).Debug("Successfully parsed RSS feed")
		return nil
	}
	return fmt.Errorf("failed to fetch RSS feed after retries")
}

// parseRSS parses the RSS feed from the given reader and processes each item.
func parseRSS(reader io.Reader) error {
	logrus.Debug("Starting to parse RSS feed")
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			logrus.Debug("Finished parsing RSS feed")
			break
		}
		if err != nil {
			logrus.Error("Error decoding XML")
			return err
		}

		if elem, ok := token.(xml.StartElement); ok && elem.Name.Local == "item" {
			var item Item
			if err := decoder.DecodeElement(&item, &elem); err != nil {
				logrus.Error("Error decoding XML element")
				return err
			}
			logrus.WithField("item", item).Debug("Parsed RSS item")
			processItem(item)
		}
	}
	return nil
}

func processItem(item Item) {
	defer recoverFromPanic()
	results <- item
	logrus.WithField("item", item).Debug("Processed item and sent to results channel")
}

func recoverFromPanic() {
	if r := recover(); r != nil {
		logrus.Errorf("Recovered from panic: %v", r)
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	itemMutex.Lock()
	defer itemMutex.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintln(w, "<html><head><title>RSS Feed Summary</title></head><body><h1>RSS Feed Summary</h1><ul>")
	for _, item := range items {
		fmt.Fprintf(w, "<li>%s: %s</li>", item.PubDate, item.Title)
	}
	fmt.Fprintln(w, "</ul></body></html>")
	logrus.Debug("Served HTML page with items")
}
