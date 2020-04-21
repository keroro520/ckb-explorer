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
	"time"

	"github.com/hpcloud/tail"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// CommandLine Flags
	Namespace       string
	Listen          string
	CkbLogToFile    string
	CkbLogToJournal string

	// Global Variables
	metrics_chan            chan Metric
	seen                    map[string]InstrumentSet
	propagations            map[string]Propagation
	last_prune_propagations time.Time

	PROPAGATION_PERCENTAGES = [...]uint64{50, 80, 90, 95, 99}
)

const (
	PROPAGATION_THRESHOLD uint64 = 50
)

type Metric struct {
	Topic  string
	Tags   map[string]string
	Fields map[string]uint64
}

type InstrumentSet struct {
	counter   prometheus.Counter
	gauge     prometheus.Gauge
	histogram prometheus.Histogram
}

type Propagation struct {
	timestamp    time.Time
	accumulative uint64
}

func NewInstrumentSet(topic string, name string, tags map[string]string) InstrumentSet {
	namespaceC := fmt.Sprintf("%s_counter", Namespace)
	namespaceG := fmt.Sprintf("%s_gauge", Namespace)
	namespaceH := fmt.Sprintf("%s_hist", Namespace)
	return InstrumentSet{
		counter: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespaceC, Subsystem: topic, Name: name,
			ConstLabels: tags,
		}),
		gauge: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespaceG, Subsystem: topic, Name: name,
			ConstLabels: tags,
		}),
		histogram: promauto.NewHistogram(prometheus.HistogramOpts{
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

func NewPropagation(timestamp time.Time) Propagation {
	return Propagation{
		timestamp:    timestamp,
		accumulative: 1,
	}
}

func ready() {
	if (len(CkbLogToFile) == 0) == (len(CkbLogToJournal) == 0) {
		log.Fatal("Must provide only one of ckb-log-to-file and ckb-log-to-journal")
	}
}

func startInFile(filepath string) {
	log.Printf("[INFO][ckb_exporter] start monitoring logfile %s", filepath)
	for {
		tailer, err := tail.TailFile(filepath, tail.Config{
			ReOpen:   true,
			Follow:   true,
			Location: &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd},
		})
		if err != nil {
			log.Printf("error on tailing %s: %v", filepath, err)
            return
		}
		for line := range tailer.Lines {
			if line.Err != nil {
				log.Printf("[ERROR][ckb_exporter] error on tailing %v", line.Err)
				continue
			}
			index := strings.Index(line.Text, "ckb-metrics")
			if index == -1 {
				continue
			}

			var metric Metric
			err := json.Unmarshal([]byte(strings.TrimSpace(line.Text[index+12:])), &metric)
			if err != nil {
				log.Printf("[ERROR][ckb_exporter] error on unmarshal %s: %v", line.Text, err)
				continue
			}

			metrics_chan <- metric
		}
		log.Printf("[INFO][ckb_exporter] reopen %s", filepath)
	}
}

func startInJournal() {
	log.Printf("[INFO][ckb_exporter] start monitoring service %s", CkbLogToJournal)
}

func startHandle() {
	for metric := range metrics_chan {
		if metric.Topic == "propagation" {
			handle_propagation(metric)
		} else {
			handle_normal(metric)
		}
	}
}

func handle_normal(metric Metric) {
	for field, value := range metric.Fields {
		name := fmt.Sprintf("%s_%s", metric.Topic, field)
		if _, ok := seen[name]; !ok {
			seen[name] = NewInstrumentSet(metric.Topic, field, metric.Tags)
		}
		set := seen[name]
		set.Update(value)
	}
}

func handle_propagation(metric Metric) {
	// Initialize seen["propagation_xx"]
	if _, ok := seen["propagation_50"]; !ok {
		empty := make(map[string]string)
		for _, p := range PROPAGATION_PERCENTAGES {
			name := fmt.Sprintf("propagation_%d", p)
			seen[name] = NewInstrumentSet(name, "elapsed", empty)
		}
	}

	// NOTE: Currently assume all are compact block propagation
	//
	// {
	//   "topic": "propagation",
	//   "tags": { "compact_block": BLOCK_HASH },
	//   "fields" { "total_peers": TOTAL_PEERS }
	// }
	block_hash := metric.Tags["compact_block"]
	total_peers := metric.Fields["total_peers"]
	timestamp := time.Now()

	// Initialize propagations[BLOCK_HASH] when we firstly seen this block
	if _, ok := propagations[block_hash]; !ok {
		propagations[block_hash] = NewPropagation(timestamp)
	}

	// Accumalate propagations
	prop := propagations[block_hash]
	prop.accumulative += 1
	propagations[block_hash] = prop
	if prop.accumulative < PROPAGATION_THRESHOLD {
		return
	}

	// Statistics
	for _, p := range PROPAGATION_PERCENTAGES {
		if (prop.accumulative-1)*100 < total_peers*p {
			if prop.accumulative*100 >= total_peers*p {
				name := fmt.Sprintf("propagation_%d", p)
				elapsed := timestamp.Sub(prop.timestamp).Milliseconds()

				set := seen[name]
				set.Update(uint64(elapsed))
			}
		}
	}

	// Timely clear staled items
	if timestamp.Sub(last_prune_propagations) > 5*60*time.Second {
		last_prune_propagations = timestamp
		for hash, prop := range propagations {
			if timestamp.Sub(prop.timestamp) > 5*60*time.Second {
				delete(propagations, hash)
			}
		}
	}
}

func init() {
	flag.StringVar(&Namespace, "namespace", "ckb", "namespace of metrics")
	flag.StringVar(&Listen, "listen", "127.0.0.1:8316", "exported address to prometheus server")
	flag.StringVar(&CkbLogToFile, "ckb-log-to-file", "", "the path to ckb log file")
	flag.StringVar(&CkbLogToJournal, "ckb-log-to-journal", "", "the service name to ckb")
	flag.Parse()

	metrics_chan = make(chan Metric)
	seen = make(map[string]InstrumentSet)
	propagations = make(map[string]Propagation)
	last_prune_propagations = time.Now()
}

func main() {
	ready()

	if len(CkbLogToFile) != 0 {
		for _, filepath := range strings.Split(CkbLogToFile, ",") {
			filepath = strings.TrimSpace(filepath)
			go startInFile(filepath)
		}
	} else {
		go startInJournal()
	}

	go startHandle()

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
