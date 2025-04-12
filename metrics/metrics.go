package processor2

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Contador de imágenes procesadas
	ImagesProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "image_processor_images_processed_total",
			Help: "The total number of processed images",
		},
		[]string{"status", "format"},
	)

	// Histograma para tiempo de procesamiento
	ProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "image_processor_duration_seconds",
			Help:    "Image processing duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"format"},
	)

	// Gauge para el tamaño de la cola de trabajos
	QueueSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "image_processor_queue_size",
			Help: "Current size of the job queue",
		},
	)

	// Histograma para la reducción de tamaño de las imágenes
	SizeReduction = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "image_processor_size_reduction_percent",
			Help:    "Percentage of size reduction achieved",
			Buckets: []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 95, 99},
		},
		[]string{"format"},
	)
)

// InitMetricsServer inicia un servidor HTTP para exponer métricas a Prometheus
func InitMetricsServer(port string) {
	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":"+port, nil)
}