package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type Item struct {
	Title   string `xml:"title"`
	PubDate string `xml:"pubDate"`
}

type RSS struct {
	Items []Item `xml:"channel>item"`
}

var (
	results   = make(chan Item)
	htmlMutex sync.Mutex
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
	switch logLevel {
	case "DEBUG":
		logrus.SetLevel(logrus.DebugLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}
}

func main() {
	rssURLs := []string{
		"https://feeds.bbci.co.uk/news/rss.xml",
		"https://www1.cbn.com/rss-cbn-articles-cbnnews.xml",
	}

	for _, url := range rssURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			if err := fetchRSS(u); err != nil {
				logrus.WithFields(logrus.Fields{"url": u}).Error("Error processing RSS feed")
			}
		}(url)
	}

	// Wait for all fetch operations to complete, then collect items from results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	http.HandleFunc("/main.go", serveHTML)
	logrus.Info("Serving on http://localhost:8080/main.go")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logrus.Fatalf("Failed to start server: %v", err)
	}
}

// fetchRSS fetches the RSS feed from the specified URL and processes the items.
func fetchRSS(url string) error {
	for i := 0; i < 3; i++ {
		resp, err := http.Get(url)
		if err != nil {
			logrus.WithField("url", url).Error("Failed to fetch RSS feed")
			time.Sleep(time.Duration(2<<i) * time.Second)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("received non-200 response: %s", resp.Status)
		}

		if err := parseRSS(resp.Body); err != nil {
			logrus.WithField("url", url).Error("Error parsing RSS feed")
			return err
		}

		return nil
	}
	return fmt.Errorf("failed to fetch RSS feed after retries")
}

// parseRSS parses the RSS feed from the given reader and processes each item.
func parseRSS(reader io.Reader) error {
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
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
			processItem(item) // This will run in its own goroutine
		}
	}
	return nil
}

func processItem(item Item) {
	defer recoverFromPanic()
	htmlMutex.Lock()
	items = append(items, item)
	htmlMutex.Unlock()
}

func recoverFromPanic() {
	if r := recover(); r != nil {
		logrus.Errorf("Recovered from panic: %v", r)
	}
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	htmlMutex.Lock()
	defer htmlMutex.Unlock()

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintln(w, "<html><head><title>RSS Feed Summary</title></head><body><h1>RSS Feed Summary</h1><ul>")
	for _, item := range items {
		fmt.Fprintf(w, "<li>%s: %s</li>", item.PubDate, item.Title)
	}
	fmt.Fprintln(w, "</ul></body></html>")
}
