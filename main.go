package main

import (
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
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/nats-io/go-nats"
)

// MaxRequestSize is the maximum size of the POST body
const MaxRequestSize = 16384

type config struct {
	User string
	Pass string
	Host string
	Port int
	Test string
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
	if cfg.Test != "" {
		log.Printf("Running in test mode, subscribing to topic %s", cfg.Test)
		s, err := nc.Subscribe(cfg.Test, func(msg *nats.Msg) {
			log.Printf("Received message [%s] %s", msg.Subject, string(msg.Data))
			if msg.Reply != "" {
				reply := []byte(fmt.Sprintf("{ \"pong\": %s }", string(msg.Data)))
				if err := nc.Publish(msg.Reply, reply); err != nil {
					log.Printf("Error replying to message [%s]: %+v", msg.Subject, err)
				}
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		defer s.Unsubscribe()
		log.Fatal(waitForInterrupt())
	}
	addRoutes(nc)
	log.Print("Waiting for requests on port 8080")
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

// addRoutes adds the /topics and /requests routes
func addRoutes(p *nats.Conn) {
	r := mux.NewRouter()
	r.Methods("POST").Path("/topics/{topic}").Headers("Content-Type", "application/json").Handler(
		handlers.LoggingHandler(os.Stdout, handler(p, topic)))
	r.Methods("POST").Path("/requests/{topic}").Headers("Content-Type", "application/json").Handler(
		handlers.LoggingHandler(os.Stdout, handler(p, request)))
	http.Handle("/", r)
}

// forPublisher creates a http.Handler for the given publisher
func handler(pub *nats.Conn, f func(pub *nats.Conn, topic string, data []byte) (response []byte, status int, err error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		topic, data, code, err := decode(r)
		if err == nil {
			data, code, err = f(pub, topic, data)
		}
		if data != nil {
			w.Header().Add("Content-Type", "application/json")
		}
		w.WriteHeader(code)
		if err != nil {
			log.Print("NATS Error: ", err)
			w.Write([]byte(err.Error()))
		} else {
			w.Write(data)
		}
	})
}

// decode the request body, get the topic and message
func decode(r *http.Request) (topic string, data []byte, status int, err error) {
	// Always read the body to completion, and close it, before leaving
	if r.Body != nil {
		defer func() {
			io.Copy(ioutil.Discard, r.Body)
			r.Body.Close()
		}()
	}
	// Get topic from URL
	pvars := mux.Vars(r)
	var ok bool
	topic, ok = pvars["topic"]
	if !ok || topic == "" {
		return "", nil, http.StatusNotFound, errors.New("Missing topic")
	}
	// Check if there is a message body
	if r.Body == nil {
		return "", nil, http.StatusNotAcceptable, errors.New("missing topic body")
	}
	// Check content
	data, err = ioutil.ReadAll(io.LimitReader(r.Body, MaxRequestSize))
	if err != nil {
		return "", nil, http.StatusBadRequest, err
	}
	return topic, data, http.StatusOK, nil
}

// Topic handler
func topic(pub *nats.Conn, topic string, data []byte) (response []byte, status int, err error) {
	if err := pub.Publish(topic, data); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return nil, http.StatusNoContent, nil
}

// Request handler
func request(pub *nats.Conn, topic string, data []byte) (response []byte, status int, err error) {
	msg, err := pub.Request(topic, data, 4*time.Second)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return msg.Data, http.StatusOK, nil
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
