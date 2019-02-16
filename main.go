package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/nats-io/go-nats"
)

// MaxRequestSize is the maximum size of the POST body
const MaxRequestSize = 8192

type config struct {
	User string
	Pass string
	Host string
	Port int
	Test string
}

type publisher interface {
	Publish(subject string, v interface{}) error
}

// Naive HTTP => NATS gateway
// Receives GET requests to /topic/{topic}, and publishes the query parameters to the topic.
func main() {
	var cfg config
	if err := cfg.read(); err != nil {
		log.Fatal("Error reading config: ", err)
	}
	url := fmt.Sprintf("tls://%s:%s@%s:%d", cfg.User, cfg.Pass, cfg.Host, cfg.Port)
	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatal("Error connecting to server: ", err)
	}
	defer nc.Close()
	ec, err := nats.NewEncodedConn(nc, nats.JSON_ENCODER)
	if err != nil {
		log.Fatal("Error setting JSON encoder: ", err)
	}
	defer ec.Close()
	if cfg.Test != "" {
		log.Printf("Running in test mode, subscribing to topic %s", cfg.Test)
		s, err := ec.Subscribe(cfg.Test, func(req map[string]string) {
			log.Printf("Received message %+v", req)
		})
		if err != nil {
			log.Fatal(err)
		}
		defer s.Unsubscribe()
		log.Fatal(waitForInterrupt())
	}
	addRoutes(ec)
	log.Print("Waiting for requests on port 8080, URL /topics/{topic}")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// wait for Ctrl+C
func waitForInterrupt() error {
	var waiter sync.WaitGroup
	var result os.Signal
	waiter.Add(1)
	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, os.Interrupt)
	go func() {
		result = <-sigChannel
		waiter.Done()
	}()
	waiter.Wait()
	return fmt.Errorf("Signal received: %+v", result)
}

func addRoutes(p publisher) {
	r := mux.NewRouter()
	r.Methods("POST").Path("/topics/{topic}").Headers("Content-Type", "application/json").Handler(
		handlers.LoggingHandler(os.Stdout, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			code, err := topic(p, w, r)
			w.WriteHeader(code)
			if err != nil {
				log.Print("NATS Error: ", err)
				w.Write([]byte(err.Error()))
			}
		})))
	http.Handle("/", r)
}

func topic(p publisher, w http.ResponseWriter, r *http.Request) (int, error) {
	// Always read the body to completion, and close it, before leaving
	if r.Body != nil {
		defer func() {
			io.Copy(ioutil.Discard, r.Body)
			r.Body.Close()
		}()
	}
	// Get topic from URL
	pvars := mux.Vars(r)
	topic, ok := pvars["topic"]
	if !ok || topic == "" {
		return http.StatusNotFound, errors.New("Missing topic")
	}
	// Check if there is a message body
	if r.Body == nil {
		return http.StatusNotAcceptable, errors.New("missing topic body")
	}
	// Check content
	message := make(map[string]string)
	decoder := json.NewDecoder(io.LimitReader(r.Body, MaxRequestSize))
	if err := decoder.Decode(&message); err != nil {
		return http.StatusBadRequest, err
	}
	// Publish the message to the topic
	if err := p.Publish(topic, message); err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusNoContent, nil
}

// read config from command line / environment
func (c *config) read() error {
	user := flag.String("user", "", "NATS username")
	pass := flag.String("pass", "", "NATS password")
	host := flag.String("host", "", "NATS server address")
	port := flag.Int("port", 0, "NATS server port")
	test := flag.String("test", "", "Subscribe to this topic, for testing")
	flag.Parse()
	if user == nil || *user == "" {
		v, ok := os.LookupEnv("NATS_USER")
		if !ok {
			return errors.New("Missing both -user flag and NATS_USER env var")
		}
		user = &v
	}
	if pass == nil || *pass == "" {
		v, ok := os.LookupEnv("NATS_PASS")
		if !ok {
			return errors.New("Missing both -pass flag and NATS_PASS env var")
		}
		pass = &v
	}
	if host == nil || *host == "" {
		v, ok := os.LookupEnv("NATS_HOST")
		if !ok {
			return errors.New("Missing both -pass flag and NATS_PASS env var")
		}
		host = &v
	}
	if port == nil || *port == 0 {
		v, ok := os.LookupEnv("NATS_HOST")
		if !ok {
			return errors.New("Missing both -pass flag and NATS_PASS env var")
		}
		p, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		port = &p
	}
	if test == nil || *test == "" {
		if v, ok := os.LookupEnv("NATS_TEST"); ok {
			test = &v
		}
	}
	c.User = *user
	c.Pass = *pass
	c.Host = *host
	c.Port = *port
	if test != nil {
		c.Test = *test
	}
	return nil
}
