package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/hpcloud/tail"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log"
	"net/http"
	"strings"
)

var (
	// CommandLine Flags
	Listen string
	CkbLogToFile string
	CkbLogToJournal string

	// Global Variables
	metric Metric
	seen map[string]InstrumentSet
)

type Metric struct {
	Topic string
	tags map[string]string
	fields map[string]uint64
}

type InstrumentSet struct {
	counter prometheus.Counter
	gauge prometheus.Gauge
	histogram prometheus.Histogram
}

func NewInstrumentSet(name string) InstrumentSet {
	return InstrumentSet{
		counter:   promauto.NewCounter(prometheus.CounterOpts{Name: name}),
		gauge:     promauto.NewGauge(prometheus.GaugeOpts{Name: name}),
		histogram: promauto.NewHistogram(prometheus.HistogramOpts{Name: name}),
	}
}

func (it *InstrumentSet) Update(value uint64) {
	float := float64(value)
	it.counter.Add(float)
	it.gauge.Set(float)
	it.histogram.Observe(float)
}

func ready() {
	if (len(CkbLogToFile) == 0) == (len(CkbLogToJournal) == 0) {
		log.Fatal("Must provide only one of ckb-log-to-file or ckb-log-to-journal")
	}
}

func StartInFile() {
	tailer, err := tail.TailFile(CkbLogToFile, tail.Config{})
	if err != nil {
		log.Fatalf("error on tailing %s: %v", CkbLogToFile, err)
	}
	for line := range tailer.Lines{
		if line.Err != nil {
			log.Printf("[ERROR][ckb-explorer] error on tailing %v", line.Err)
			continue
		}
		handle(line.Text)
	}
}

func StartInJournal() {

}

func handle(line string) {
	index := strings.Index(line, "ckb-metrics")
	if index == -1 {
		return
	}

	err := json.Unmarshal([]byte(line[index+12:]), &metric)
	if err != nil {
		log.Printf("[ERROR][ckb-explorer] error on unmarshal %s: %v", line, err)
		return
	}

	for field, value := range metric.fields {
		name := fmt.Sprintf("%s_%s", metric.Topic, field)
		if _, ok := seen[name]; !ok {
			seen[name] = NewInstrumentSet(name)
		}
		set := seen[name]
		set.Update(value)
	}
}

func init() {
	flag.StringVar(&Listen, "listen", "127.0.0.1:1943", "exported address to prometheus server")
	flag.StringVar(&CkbLogToFile, "ckb-log-to-file", "", "the path to ckb log file")
	flag.StringVar(&CkbLogToJournal, "ckb-log-to-journal", "", "the service name to ckb")
	seen = make(map[string]InstrumentSet)
}

func main() {
	ready()
	if len(CkbLogToFile) != 0 {
		go StartInFile()
	} else {
		go StartInJournal()
	}
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(Listen, nil))
}