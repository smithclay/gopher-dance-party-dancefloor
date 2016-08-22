package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/garyburd/redigo/redis"
	"github.com/newrelic/go-agent"
)

var (
	redisAddress   = flag.String("redis-address", ":6379", "Address to the Redis server")
	maxConnections = flag.Int("max-connections", 10, "Max connections to Redis")
	httpPort       = flag.String("port", ":5001", "Port number to listen on")
	licenseKey     = flag.String("license-key", "", "New Relic license key")
)

type Position struct {
	X float64
	Y float64
}

func handleParamsError(w http.ResponseWriter) {
	http.Error(w, "missing required params", http.StatusBadRequest)

	if txn, ok := w.(newrelic.Transaction); ok {
		txn.NoticeError(errors.New("missing required params"))
	}
}

func handleRedisError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	if txn, ok := w.(newrelic.Transaction); ok {
		txn.NoticeError(err)
	}

	log.Println("redis error", err)
}

func performRedisOperation(w http.ResponseWriter, c redis.Conn, op string, args ...interface{}) (reply interface{}, err error) {
	s := newrelic.DatastoreSegment{
		Product:    newrelic.DatastoreRedis,
		Collection: "gophers",
		Operation:  op,
	}

	if txn, ok := w.(newrelic.Transaction); ok {
		s.StartTime = newrelic.StartSegmentNow(txn)
	}
	reply, err = c.Do(op, args...)
	s.End()
	if err != nil {
		handleRedisError(w, err)
		return nil, err
	}
	return reply, nil
}

func main() {
	flag.Parse()

	config := newrelic.NewConfig("dancefloor", *licenseKey)
	app, err := newrelic.NewApplication(config)

	if err != nil {
		log.Println("error creating new relic agent", err)
	}

	redisHash := "gophers" // redis hash name where data is persisted

	redisPool := redis.NewPool(func() (redis.Conn, error) {
		c, err := redis.Dial("tcp", *redisAddress)
		if err != nil {
			log.Println("error connection to redis", err)
			return nil, err
		}
		return c, err
	}, *maxConnections)
	defer redisPool.Close()

	log.Println("Listening on port:", *httpPort)

	http.HandleFunc(newrelic.WrapHandleFunc(app, "/add", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		x := r.URL.Query().Get("x")
		y := r.URL.Query().Get("y")

		if id == "" || x == "" || y == "" {
			handleParamsError(w)
			return
		}

		c := redisPool.Get()
		defer c.Close()
		performRedisOperation(w, c, "HSETNX", redisHash, id, fmt.Sprintf("%s,%s", x, y))
		fmt.Fprintf(w, "ok")
	}))

	http.HandleFunc(newrelic.WrapHandleFunc(app, "/del", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")

		if id == "" {
			handleParamsError(w)
			return
		}

		c := redisPool.Get()
		defer c.Close()
		performRedisOperation(w, c, "HDEL", redisHash, id)
		fmt.Fprintf(w, "ok")
	}))

	http.HandleFunc(newrelic.WrapHandleFunc(app, "/move", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		x := r.URL.Query().Get("x")
		y := r.URL.Query().Get("y")

		if id == "" || x == "" || y == "" {
			handleParamsError(w)
			return
		}

		c := redisPool.Get()
		defer c.Close()
		performRedisOperation(w, c, "HSET", redisHash, id, fmt.Sprintf("%s, %s", x, y))
		fmt.Fprintf(w, "ok")
	}))

	http.HandleFunc(newrelic.WrapHandleFunc(app, "/fetch", func(w http.ResponseWriter, r *http.Request) {
		c := redisPool.Get()
		defer c.Close()
		values, err := redis.Values(performRedisOperation(w, c, "HGETALL", redisHash))
		if err != nil {
			return
		}
		returns := make(map[string]interface{})
		for i := 0; i < len(values); i += 2 {
			id, _ := redis.String(values[i], nil)
			value, _ := redis.String(values[i+1], nil)
			positions := strings.Split(value, ",")
			x, _ := strconv.ParseFloat(positions[0], 64)
			y, _ := strconv.ParseFloat(positions[1], 64)
			position := Position{X: x, Y: y}
			returns[id] = position
		}
		json, err := json.Marshal(returns)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			if txn, ok := w.(newrelic.Transaction); ok {
				txn.NoticeError(err)
			}

			log.Println("json marshal error", err)
			return
		}
		fmt.Fprintf(w, "%s", string(json))
	}))

	http.ListenAndServe(*httpPort, nil)
}
