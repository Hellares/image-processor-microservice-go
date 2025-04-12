package processor

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"image-processing-microservice-go/internal/config"
	"image-processing-microservice-go/internal/logger"

	"github.com/sirupsen/logrus"
	processor2 "image-processing-microservice-go/metrics"
)

// ImageJob representa un trabajo de procesamiento de imagen
type ImageJob struct {
	MessageID    string
	Filename     string
	ImageData    []byte
	Mimetype     string
	Preset       config.ImagePreset
	CompanyID    string
	UserID       string
	Module       string
	OriginalSize int
	// No incluimos el objeto delivery completo para evitar problemas de serialización
	CorrelationID string // Solo guardamos el correlationId para responder
	ReplyQueue    string // Cola específica para respuestas si existe
}

// ImageResult representa el resultado del procesamiento
type ImageResult struct {
	MessageID     string
	CorrelationID string
	Filename      string
	ProcessedData []byte
	CompanyID     string
	UserID        string
	Module        string
	OriginalSize  int
	ProcessedSize int
	Info          map[string]interface{}
	Error         error
	Duration      time.Duration
	ReplyQueue    string // Cola específica para respuestas
}

// WorkerPool administra un conjunto de workers para procesamiento paralelo
type WorkerPool struct {
	jobQueue    chan ImageJob
	resultQueue chan ImageResult
	workersCount int
	log         *logrus.Entry
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewWorkerPool crea un nuevo pool de trabajadores
func NewWorkerPool() *WorkerPool {
	// Determinar número óptimo de workers basado en CPU
	numCPU := runtime.NumCPU()
	configWorkers := config.Config.MaxWorkers
	
	// Si MAX_WORKERS es 0 o negativo, usar núcleos - 1 (dejar un núcleo para el sistema)
	optimalWorkers := numCPU - 1
	if optimalWorkers < 1 {
		optimalWorkers = 1
	}
	
	// Usar el valor configurado si es válido
	if configWorkers > 0 {
		optimalWorkers = configWorkers
	}
	
	log.Printf("Iniciando WorkerPool con %d workers (CPU cores: %d)", optimalWorkers, numCPU)
	
	// Definir capacidades de los canales (buffer)
	jobQueueSize := optimalWorkers * 1 //!incrementar cuando se tenga mas cpus
	resultQueueSize := optimalWorkers * 1 //!incrementar cuando se tenga mas cpus
	
	ctx, cancel := context.WithCancel(context.Background())
	
	return &WorkerPool{
		jobQueue:     make(chan ImageJob, jobQueueSize),
		resultQueue:  make(chan ImageResult, resultQueueSize),
		workersCount: optimalWorkers,
		log:          logger.Log.WithField("component", "worker-pool"),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start inicia los workers
func (wp *WorkerPool) Start() {
	wp.log.Infof("Iniciando pool con %d workers", wp.workersCount)
	
	// Iniciar workers
	for i := 0; i < wp.workersCount; i++ {
		wp.wg.Add(1)
		workerID := i + 1
		go wp.runWorker(workerID)
	}
}

// Stop detiene todos los workers
func (wp *WorkerPool) Stop() {
	wp.log.Info("Deteniendo worker pool...")
	wp.cancel()
	
	// Esperar a que terminen todos los workers
	wp.log.Info("Esperando a que finalicen los workers...")
	wp.wg.Wait()
	
	// Cerrar canales
	close(wp.jobQueue)
	close(wp.resultQueue)
	wp.log.Info("Worker pool detenido")
}

// EnqueueJob añade un trabajo al pool
func (wp *WorkerPool) EnqueueJob(job ImageJob) error {
	select {
	case wp.jobQueue <- job:
		//!metricas prometheus: Actualizar métrica del tamaño de la cola
		processor2.QueueSize.Set(float64(len(wp.jobQueue)))
		return nil
	case <-time.After(5 * time.Second):
		return ErrJobQueueFull
	}
}

// GetResults devuelve el canal de resultados
func (wp *WorkerPool) GetResults() <-chan ImageResult {
	return wp.resultQueue
}

// runWorker ejecuta un worker individual
func (wp *WorkerPool) runWorker(id int) {
	defer wp.wg.Done()
	
	logger := wp.log.WithField("worker_id", id)
	logger.Info("Worker iniciado")
	
	// Definir timeout por imagen desde la configuración
	processingTimeout := time.Duration(config.Config.ProcessingTimeout) * time.Second
	
	for {
		select {
		case <-wp.ctx.Done():
			logger.Info("Worker recibió señal de finalización")
			return
			
		case job, ok := <-wp.jobQueue:
			if !ok {
				logger.Info("Canal de trabajos cerrado, finalizando worker")
				return
			}
			
			//! Actualizar métrica del tamaño de la cola después de consumir un trabajo
            processor2.QueueSize.Set(float64(len(wp.jobQueue)))

			// Crear contexto con timeout para este trabajo
			jobCtx, cancel := context.WithTimeout(wp.ctx, processingTimeout)
			
			// Registrar inicio del procesamiento
			startTime := time.Now()
			logger.WithField("message_id", job.MessageID).
				Infof("Iniciando procesamiento de imagen %s (%d bytes)", 
					job.Filename, len(job.ImageData))
			
			// Ejecutar procesamiento con timeout
			result := ImageResult{
				MessageID:     job.MessageID,
				CorrelationID: job.CorrelationID,
				Filename:      job.Filename,
				CompanyID:     job.CompanyID,
				UserID:        job.UserID,
				Module:        job.Module,
				OriginalSize:  job.OriginalSize,
				Duration:      0,
				ReplyQueue:    job.ReplyQueue,
			}
			
			// Crear canal para recibir resultado del procesamiento
			done := make(chan struct{})
			
			go func() {
				defer close(done)
				
				// Procesar imagen 
				// Nota: Cambiamos cómo obtenemos el procesador para evitar el problema de dependencia circular
				processor := GetImageProcessor() // Función auxiliar que veremos más abajo
				if processor != nil {
					processedData, info, err := processor.ProcessImageWithCtx(jobCtx, job.ImageData, job.Mimetype, job.Preset)
					result.ProcessedData = processedData
					if processedData != nil {
						result.ProcessedSize = len(processedData)
					}
					result.Info = info
					result.Error = err
				} else {
					result.Error = fmt.Errorf("no se pudo obtener procesador de imágenes")
				}
			}()
			
			// Esperar resultado o timeout
			select {
			case <-jobCtx.Done():
				// El contexto expiró (timeout) o fue cancelado
				result.Error = jobCtx.Err()
				logger.WithField("message_id", job.MessageID).
					Warnf("Timeout en procesamiento de imagen después de %s", processingTimeout)
					
			case <-done:
				// Procesamiento completado
				result.Duration = time.Since(startTime)
				
				if result.Error != nil {
					logger.WithField("message_id", job.MessageID).
						WithError(result.Error).
						Error("Error procesando imagen")
				} else {
					logger.WithField("message_id", job.MessageID).
						WithField("duration_ms", result.Duration.Milliseconds()).
						Infof("Imagen procesada: %d bytes -> %d bytes", 
							len(job.ImageData), result.ProcessedSize)
				}
			}
			
			// Liberar recursos del contexto
			cancel()
			
			// Enviar resultado
			select {
			case wp.resultQueue <- result:
				// Resultado enviado correctamente
			case <-time.After(2 * time.Second):
				// No se pudo enviar el resultado (canal lleno)
				logger.WithField("message_id", job.MessageID).
					Warn("No se pudo enviar resultado, canal lleno")
			}
		}
	}
}

// Error personalizado para cola llena
var ErrJobQueueFull = fmt.Errorf("cola de trabajos llena")

// Variable global para almacenar una única instancia del procesador
var imageProcessorInstance *ImageProcessor
var imageProcessorMutex sync.Mutex

// GetImageProcessor obtiene la instancia global del procesador
// Esta función ayuda a evitar dependencias circulares
func GetImageProcessor() *ImageProcessor {
	imageProcessorMutex.Lock()
	defer imageProcessorMutex.Unlock()
	return imageProcessorInstance
}

// SetImageProcessor establece la instancia global del procesador
func SetImageProcessor(processor *ImageProcessor) {
	imageProcessorMutex.Lock()
	defer imageProcessorMutex.Unlock()
	imageProcessorInstance = processor
}