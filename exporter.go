package exporter

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
)

// Exporter handles serving the metrics
type Exporter struct {
	addr        string
	gearmanAddr string
	logger      *zap.Logger
}

// OptionsFunc is a function passed to new for setting options on a new Exporter.
type OptionsFunc func(*Exporter) error

// New creates an exporter.
func New(options ...OptionsFunc) (*Exporter, error) {
	e := &Exporter{
		addr:        "127.0.0.1:9418",
		gearmanAddr: "127.0.0.1:4730",
	}

	for _, f := range options {
		if err := f(e); err != nil {
			return nil, errors.Wrap(err, "failed to set options")
		}
	}

	if e.logger == nil {
		l, err := NewLogger()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create logger")
		}
		e.logger = l
	}

	return e, nil
}

// SetLogger creates a function that will set the logger.
// Generally only used when create a new Exporter.
func SetLogger(l *zap.Logger) func(*Exporter) error {
	return func(e *Exporter) error {
		e.logger = l
		return nil
	}
}

// SetAddress creates a function that will set the listening address.
// Generally only used when create a new Exporter.
func SetAddress(addr string) func(*Exporter) error {
	return func(e *Exporter) error {
		//host, port, err := net.SplitHostPort(addr)
		//if err != nil {
		//	return errors.Wrapf(err, "invalid address")
		//}
		//e.addr = net.JoinHostPort(host, port)
		e.addr = addr
		return nil
	}
}

// SetGearmanAddress creates a function that will set the address to contact gearman.
// Generally only used when create a new Exporter.
func SetGearmanAddress(addr string) func(*Exporter) error {
	return func(e *Exporter) error {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return errors.Wrapf(err, "invalid address")
		}
		e.gearmanAddr = net.JoinHostPort(host, port)
		return nil
	}
}

var healthzOK = []byte("ok\n")

func (e *Exporter) healthz(w http.ResponseWriter, r *http.Request) {
	// TODO: check if we can contact gearman?
	w.Write(healthzOK)
}

// Run starts the http server and collecting metrics. It generally does not return.
func (e *Exporter) Run() error {

	c := e.newCollector(newGearman(e.gearmanAddr))
	if err := prometheus.Register(c); err != nil {
		return errors.Wrap(err, "failed to register metrics")
	}

	stopChan := make(chan os.Signal)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	var g errgroup.Group

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
                        <head><title>Gearman Exporter</title></head>
                        <body>
                        <h1>Gearman Exporter</h1>
                        <p><a href="` + "/metrics" + `">Metrics</a></p>
                        </body>
                        </html>`))
	})
	server := http.Server{
		Handler: mux, // http.DefaultServeMux,
	}
	os.Remove(e.addr)

	listener, err := net.Listen("unix", e.addr)
	if err != nil {
		panic(err)
	}

	g.Go(func() error {
		// TODO: allow TLS
		return server.Serve(listener)
	})
	g.Go(func() error {
		<-stopChan
		// XXX: should shutdown time be configurable?
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		return nil
	})

	if err := g.Wait(); err != http.ErrServerClosed {
		return errors.Wrap(err, "failed to run server")
	}

	return nil
}
