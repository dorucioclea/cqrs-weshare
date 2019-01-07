package main

import (
	"log"
	"net/http"
	"os"
	"time"
	"strings"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/go-redis/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	
	zipkin "github.com/openzipkin-contrib/zipkin-go-opentracing"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
)


// LoggerMiddleware add logger and metrics
func LoggerMiddleware(inner http.HandlerFunc, name string, histogram *prometheus.HistogramVec, counter *prometheus.CounterVec) http.Handler {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		start := time.Now()
		if strings.Compare(name,"health") != 0 {
			var serverSpan opentracing.Span
			wireContext, err := opentracing.GlobalTracer().Extract(
				opentracing.HTTPHeaders,
				opentracing.HTTPHeadersCarrier(r.Header))
			if err != nil {
				log.Println(err)
				log.Println(r.Header)
			}

			// Create the span referring to the RPC client if available.
			// If wireContext == nil, a root span will be created.
			serverSpan = opentracing.StartSpan(
				name,
				ext.RPCServerOption(wireContext))

			defer serverSpan.Finish()
		}

		inner.ServeHTTP(w, r)

		if strings.Compare(name,"health") != 0 {
			time := time.Since(start)
			log.Printf(
				"%s\t%s\t%s\t%s",
				r.Method,
				r.RequestURI,
				name,
				time,
			)

			histogram.WithLabelValues(r.RequestURI).Observe(time.Seconds())
			if counter != nil {
				counter.WithLabelValues(r.RequestURI).Inc()
			}
		}
	})
}

var client *redis.Client

func main() {

	///////////////////////////////// Redis Connection ////////////////////////////////
	ruri := os.Getenv("REDIS_URI")
	client = redis.NewClient(&redis.Options{
		Addr:     ruri,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	///////////////////////////////// Zipkin Connection ////////////////////////////////
	zuri := os.Getenv("ZIPKIN_ENDPOINT")
	collector, err := zipkin.NewHTTPCollector(zuri)
	if err != nil {
		log.Printf("unable to create Zipkin HTTP collector: %+v\n", err)
		os.Exit(-1)
	}

	// Create our recorder.
	recorder := zipkin.NewRecorder(collector, false, "0.0.0.0:8080", "ws-read-cqrs")

	// Create our tracer.
	tracer, err := zipkin.NewTracer(
		recorder,
		zipkin.ClientServerSameSpan(true),
		zipkin.TraceID128Bit(true),
	)
	if err != nil {
		log.Printf("unable to create Zipkin tracer: %+v\n", err)
		os.Exit(-1)
	}

	// Explicitly set our tracer to be the default tracer.
	opentracing.InitGlobalTracer(tracer)


	///////////////////////////////// Http Connection ////////////////////////////////
	router := mux.NewRouter().StrictSlash(true)

	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "read_ws_uri_duration_seconds",
		Help: "Time to respond",
	}, []string{"uri"})

	promCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "read_ws_count",
		Help: "counter for api",
	}, []string{"uri"})

	/// Root
	var handlerStatus http.Handler
	handlerStatus = LoggerMiddleware(handlerStatusFunc, "root", histogram, nil)
	router.
		Methods("GET").
		Path("/").
		Name("root").
		Handler(handlerStatus)

	var handlerHealth http.Handler
	handlerHealth = LoggerMiddleware(handlerHealthFunc, "health", histogram, nil)
	router.
		Methods("GET").
		Path("/healthz").
		Name("health").
		Handler(handlerHealth)

	
	var handlerUsersGet http.Handler
	handlerUsersGet = LoggerMiddleware(handlerUsersFunc, "users", histogram, nil)
	router.
		Methods("GET").
		Path("/users").
		Name("users").
		Handler(handlerUsersGet)
	
	var handlerUserGet http.Handler
	handlerUserGet = LoggerMiddleware(handlerUserFunc, "user_id", histogram, nil)
	router.
		Methods("GET").
		Path("/users/{id}").
		Name("user_id").
		Handler(handlerUserGet)

	var handlerTopicsGet http.Handler
	handlerTopicsGet = LoggerMiddleware(handlerTopicsGetFunc, "topics", histogram, nil)
	router.
		Methods("GET").
		Path("/topics").
		Name("topics").
		Handler(handlerTopicsGet)
	
	var handlerTopicGet http.Handler
	handlerTopicGet = LoggerMiddleware(handlerTopicGetFunc, "topic_id", histogram, nil)
	router.
		Methods("GET").
		Path("/topics/{id}").
		Name("topic_id").
		Handler(handlerTopicGet)

	var handlerTopicCompleteGet http.Handler
	handlerTopicCompleteGet = LoggerMiddleware(handlerTopicCompleteGetFunc, "topic_complete_id", histogram, nil)
	router.
		Methods("GET").
		Path("/topics/{id}/complete").
		Name("topic_complete_id").
		Handler(handlerTopicCompleteGet)

	var handlerMessagesGet http.Handler
	handlerMessagesGet = LoggerMiddleware(handlerMessagesGetFunc, "messages", histogram, nil)
	router.
		Methods("GET").
		Path("/messages").
		Name("messages").
		Handler(handlerMessagesGet)
	
	var handlerMessageGet http.Handler
	handlerMessageGet = LoggerMiddleware(handlerMessageGetFunc, "message_id", histogram, nil)
	router.
		Methods("GET").
		Path("/messages/{id}").
		Name("message_id").
		Handler(handlerMessageGet)
	
	// add prometheus
	prometheus.Register(histogram)
	prometheus.Register(promCounter)
	router.Methods("GET").Path("/metrics").Name("Metrics").Handler(promhttp.Handler())

	// CORS
	headersOk := handlers.AllowedHeaders([]string{"authorization", "content-type"})
	originsOk := handlers.AllowedOrigins([]string{"*"})
	methodsOk := handlers.AllowedMethods([]string{"GET", "HEAD", "POST", "PUT", "OPTIONS"})

	log.Printf("Start server...")
	http.ListenAndServe(":8080", handlers.CORS(originsOk, headersOk, methodsOk)(router))
}
