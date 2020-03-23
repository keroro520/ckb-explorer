package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/hpcloud/tail"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// CommandLine Flags
	Namespace string
	Listen string
	CkbLogToFile string
	CkbLogToJournal string

	// Global Variables
	seen map[string]InstrumentSet
)

type Metric struct {
	Topic string
	Tags map[string]string
	Fields map[string]uint64
}

type InstrumentSet struct {
	counter prometheus.Counter
	gauge prometheus.Gauge
	histogram prometheus.Histogram
}

func NewInstrumentSet(topic string, name string, tags map[string]string) InstrumentSet {
	namespaceC := fmt.Sprintf("%s_counter", Namespace)
	namespaceG := fmt.Sprintf("%s_gauge", Namespace)
	namespaceH := fmt.Sprintf("%s_hist", Namespace)
	return InstrumentSet{
		counter:   promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespaceC, Subsystem: topic, Name: name,
			ConstLabels: tags,
		}),
		gauge:   promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespaceG, Subsystem: topic, Name: name,
			ConstLabels: tags,
		}),
		histogram:   promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespaceH, Subsystem: topic, Name: name,
			ConstLabels: tags,
		}),
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
		log.Fatal("Must provide only one of ckb-log-to-file and ckb-log-to-journal")
	}
}

func startInFile() {
	log.Printf("[INFO][ckb_exporter] start monitoring logfile %s", CkbLogToFile)
	for {
		tailer, err := tail.TailFile(CkbLogToFile, tail.Config{
			ReOpen: true,
			Follow: true,
			Location: &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd},
			Logger: tail.DiscardingLogger,
		})
		if err != nil {
			log.Fatalf("error on tailing %s: %v", CkbLogToFile, err)
		}
		for line := range tailer.Lines {
			if line.Err != nil {
				log.Printf("[ERROR][ckb_exporter] error on tailing %v", line.Err)
				continue
			}
			handle(line.Text)
		}
		log.Printf("[INFO][ckb_exporter] reopen %s", CkbLogToFile)
	}
}

func startInJournal() {
	log.Printf("[INFO][ckb_exporter] start monitoring service %s", CkbLogToJournal)
}

func handle(line string) {
	index := strings.Index(line, "ckb-metrics")
	if index == -1 {
		return
	}

	var metric Metric
	err := json.Unmarshal([]byte(strings.TrimSpace(line[index+12:])), &metric)
	if err != nil {
		log.Printf("[ERROR][ckb_exporter] error on unmarshal %s: %v", line, err)
		return
	}

	for field, value := range metric.Fields {
		name := fmt.Sprintf("%s_%s", metric.Topic, field)
		if _, ok := seen[name]; !ok {
			seen[name] = NewInstrumentSet(metric.Topic, field, metric.Tags)
		}
		set := seen[name]
		set.Update(value)
	}
}

func init() {
	flag.StringVar(&Namespace, "namespace", "ckb", "namespace of metrics")
	flag.StringVar(&Listen, "listen", "127.0.0.1:8316", "exported address to prometheus server")
	flag.StringVar(&CkbLogToFile, "ckb-log-to-file", "", "the path to ckb log file")
	flag.StringVar(&CkbLogToJournal, "ckb-log-to-journal", "", "the service name to ckb")
	flag.Parse()
	seen = make(map[string]InstrumentSet)
}

func main() {
	ready()

	if len(CkbLogToFile) != 0 {
		go startInFile()
	} else {
		go startInJournal()
	}

	nodename, err := os.Hostname()
	if err != nil {
		nodename = "-"
	}
	labels := make(map[string]string)
	labels["nodename"] = nodename
	promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "node", Subsystem: "uname", Name: "info",
		ConstLabels: labels,
	})

	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(Listen, nil))
}
