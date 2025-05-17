package processor

import (
	"context"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log"
	"path/filepath"
	"strings"
	"time"

	"image-processing-microservice-go/internal/config"
	"image-processing-microservice-go/internal/logger"
	"image-processing-microservice-go/internal/rabbitmq"
	processor2 "image-processing-microservice-go/metrics"

	"github.com/disintegration/imaging"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

// ProcessorType representa los tipos de procesadores de imágenes
type ProcessorType string

const (
	ProcessorPillow          ProcessorType = "pillow"
	ProcessorPillowOptimized ProcessorType = "pillow-optimized"
	ProcessorOpenCV          ProcessorType = "opencv"
	ProcessorOpenCVAdvanced  ProcessorType = "opencv-advanced"
	ProcessorNone            ProcessorType = "none"
)

// ImageProcessor es el procesador principal de imágenes
type ImageProcessor struct {
	connection *rabbitmq.RabbitMQConnection
	workerPool *WorkerPool
	stopChan   chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	log        *logrus.Entry
}

// Message representa la estructura de los mensajes de entrada
type Message struct {
	ID          string      `json:"id"`
	Filename    string      `json:"filename"`
	Data        interface{} `json:"data,omitempty"`
	Buffer      interface{} `json:"buffer,omitempty"`
	Content     interface{} `json:"content,omitempty"`
	CompanyID   string      `json:"companyId,omitempty"`
	UserID      string      `json:"userId,omitempty"`
	Module      string      `json:"module,omitempty"`
	Options     interface{} `json:"options,omitempty"`
	Preset      string      `json:"preset,omitempty"`
	MaxWidth    int         `json:"maxWidth,omitempty"`
	MaxHeight   int         `json:"maxHeight,omitempty"`
	Quality     int         `json:"quality,omitempty"`
	Format      string      `json:"format,omitempty"`
	Mimetype    string      `json:"mimetype,omitempty"`
	Type        string      `json:"type,omitempty"`
	Command     string      `json:"command,omitempty"`
	ReplyQueue  string      `json:"replyQueue,omitempty"`  // Nueva propiedad para respuestas
}

// Response representa la estructura de las respuestas
type Response struct {
	ID            string                 `json:"id"`
	Processed     bool                   `json:"processed"`
	Filename      string                 `json:"filename,omitempty"`
	CompanyID     string                 `json:"companyId,omitempty"`
	UserID        string                 `json:"userId,omitempty"`
	Module        string                 `json:"module,omitempty"`
	OriginalSize  int                    `json:"originalSize,omitempty"`
	ProcessedSize int                    `json:"processedSize,omitempty"`
	Reduction     string                 `json:"reduction,omitempty"`
	Info          map[string]interface{} `json:"info,omitempty"`
	Duration      string                 `json:"duration,omitempty"`
	Success       bool                   `json:"success"`
	Error         string                 `json:"error,omitempty"`
	Data          string                 `json:"data,omitempty"`
	ProcessedData string                 `json:"processedData,omitempty"`
}

// NewImageProcessor crea una nueva instancia del procesador de imágenes
func NewImageProcessor(connection *rabbitmq.RabbitMQConnection) *ImageProcessor {
	if connection == nil {
		connection = rabbitmq.NewRabbitMQConnection(config.Config.GetRabbitMQURLs()[0])
		err := connection.Connect()
		if err != nil {
			log.Printf("Error al conectar: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	
	processor := &ImageProcessor{
		connection: connection,
		workerPool: NewWorkerPool(),
		stopChan:   make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
		log:        logger.Log.WithField("component", "image-processor"),
	}
	
	// Registrar el procesador en la instancia global
	SetImageProcessor(processor)
	
	// Iniciar worker pool
	processor.workerPool.Start()
	
	// Iniciar procesamiento de resultados
	go processor.processResults()
	
	return processor
}

// min devuelve el mínimo de dos enteros
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Stop detiene el procesador y sus componentes
func (p *ImageProcessor) Stop() {
	p.log.Info("Deteniendo procesador de imagenes")
	p.cancel()
	close(p.stopChan)
	p.workerPool.Stop()
	p.log.Info("Procesador de imagenes detenido")
}

// processResults procesa los resultados del worker pool
// func (p *ImageProcessor) processResults() {
// 	p.log.Info("Iniciando procesamiento de resultados")
	
// 	for {
// 		select {
// 		case <-p.stopChan:
// 			p.log.Info("Deteniendo procesamiento de resultados")
// 			return
			
// 		case result, ok := <-p.workerPool.GetResults():
// 			if !ok {
// 				// Canal cerrado
// 				p.log.Info("Canal de resultados cerrado")
// 				return
// 			}
			
// 			// Crear respuesta basada en el resultado
// 			response := Response{
// 				ID:            result.MessageID,
// 				Processed:     true,
// 				Filename:      result.Filename,
// 				CompanyID:     result.CompanyID,
// 				UserID:        result.UserID,
// 				Module:        result.Module,
// 				OriginalSize:  result.OriginalSize,
// 				ProcessedSize: result.ProcessedSize,
// 				Success:       result.Error == nil,
// 				Info:          result.Info,
// 				Duration:      fmt.Sprintf("%.2fms", float64(result.Duration.Milliseconds())),
// 			}
			 
// 			// Manejar posibles errores
// 			if result.Error != nil {
// 				response.Error = result.Error.Error()
// 				p.log.WithField("message_id", result.MessageID).
// 					WithError(result.Error).
// 					Error("Error procesando imagen")
// 			} else {
// 				// Calcular porcentaje de reducción
// 				if result.OriginalSize > 0 && result.ProcessedSize > 0 {
// 					reductionPercent := float64(result.OriginalSize-result.ProcessedSize) / float64(result.OriginalSize) * 100
// 					response.Reduction = fmt.Sprintf("%.2f%%", reductionPercent)
					
// 					p.log.WithField("message_id", result.MessageID).
// 						Infof("Imagen procesada: %s - Reducción: %.2f%%, Tamaño: %.2fKB -> %.2fKB, Duración: %.2fms",
// 							result.Filename, reductionPercent, 
// 							float64(result.OriginalSize)/1024, 
// 							float64(result.ProcessedSize)/1024, 
// 							float64(result.Duration.Milliseconds()))
// 				}
				
// 				// Añadir datos procesados si existen
// 				if len(result.ProcessedData) > 0 {
// 					response.ProcessedData = base64.StdEncoding.EncodeToString(result.ProcessedData)
// 				}
// 			}
			
// 			// Usar el correlationID o el messageID como fallback
// 			correlationID := result.CorrelationID
// 			if correlationID == "" {
// 				correlationID = result.MessageID
// 				p.log.WithField("message_id", result.MessageID).
// 					Warn("Usando MessageID como CorrelationID para respuesta")
// 			}
			
// 			// Enviar respuesta a RabbitMQ
// 			if correlationID != "" {
// 				// Si hay una cola de respuesta específica, usarla
// 				queueOut := config.Config.QueueOut
// 				if result.ReplyQueue != "" {
// 					queueOut = result.ReplyQueue
// 					p.log.WithFields(logrus.Fields{
// 						"message_id": result.MessageID,
// 						"reply_queue": result.ReplyQueue,
// 					}).Debug("Usando cola de respuesta específica")
// 				}
				
// 				err := p.publishResponse(response, correlationID, queueOut)
// 				if err != nil {
// 					p.log.WithError(err).
// 						WithField("message_id", result.MessageID).
// 						Error("Error al publicar respuesta en RabbitMQ")
// 				} else {
// 					p.log.WithFields(logrus.Fields{
// 						"message_id": result.MessageID,
// 						"queue": queueOut,
// 					}).Debug("Respuesta enviada correctamente")
// 				}
// 			} else {
// 				p.log.WithField("message_id", result.MessageID).
// 					Error("No se puede enviar respuesta, no hay ID para correlación")
// 			}
// 		}
// 	}
// }

func (p *ImageProcessor) processResults() {
	p.log.Info("Iniciando procesamiento de resultados")
	
	for {
		select {
		case <-p.stopChan:
			p.log.Info("Deteniendo procesamiento de resultados")
			return
			
		case result, ok := <-p.workerPool.GetResults():
			if !ok {
				// Canal cerrado
				p.log.Info("Canal de resultados cerrado")
				return
			}
			
			// Crear respuesta basada en el resultado
			response := Response{
				ID:            result.MessageID,
				Processed:     true,
				Filename:      result.Filename,
				CompanyID:     result.CompanyID,
				UserID:        result.UserID,
				Module:        result.Module,
				OriginalSize:  result.OriginalSize,
				ProcessedSize: result.ProcessedSize,
				Success:       result.Error == nil,
				Info:          result.Info,
				Duration:      fmt.Sprintf("%.2fms", float64(result.Duration.Milliseconds())),
			}
			
			// Determinar estado para métricas
			status := "success"
			format := "unknown"
			if result.Info != nil {
				if fmt, ok := result.Info["newFormat"].(string); ok {
					format = fmt
				}
			}
			
			// Manejar posibles errores
			if result.Error != nil {
				status = "error"
				response.Error = result.Error.Error()
				p.log.WithField("message_id", result.MessageID).
					WithError(result.Error).
					Error("Error procesando imagen")
					
				// Registrar error en Prometheus
				processor2.ImagesProcessed.WithLabelValues(status, format).Inc()
			} else {
				// Calcular porcentaje de reducción
				if result.OriginalSize > 0 && result.ProcessedSize > 0 {
					reductionPercent := float64(result.OriginalSize-result.ProcessedSize) / float64(result.OriginalSize) * 100
					response.Reduction = fmt.Sprintf("%.2f%%", reductionPercent)
					
					p.log.WithField("message_id", result.MessageID).
						Infof("Imagen procesada: %s - Reduccion: %.2f%%, Tamanio: %.2fKB -> %.2fKB, Duracion: %.2fms",
							result.Filename, reductionPercent, 
							float64(result.OriginalSize)/1024, 
							float64(result.ProcessedSize)/1024, 
							float64(result.Duration.Milliseconds()))
							
					// Registrar métricas en Prometheus
					processor2.ImagesProcessed.WithLabelValues(status, format).Inc()
					processor2.ProcessingDuration.WithLabelValues(format).Observe(float64(result.Duration.Seconds()))
					processor2.SizeReduction.WithLabelValues(format).Observe(reductionPercent)
				} else {
					// Registrar procesamiento sin reducción
					processor2.ImagesProcessed.WithLabelValues(status, format).Inc()
					processor2.ProcessingDuration.WithLabelValues(format).Observe(float64(result.Duration.Seconds()))
				}
				
				// Añadir datos procesados si existen
				if len(result.ProcessedData) > 0 {
					response.ProcessedData = base64.StdEncoding.EncodeToString(result.ProcessedData)
				}
			}
			
			// Usar el correlationID o el messageID como fallback
			correlationID := result.CorrelationID
			if correlationID == "" {
				correlationID = result.MessageID
				p.log.WithField("message_id", result.MessageID).
					Warn("Usando MessageID como CorrelationID para respuesta")
			}
			
			// Enviar respuesta a RabbitMQ
			if correlationID != "" {
				// Si hay una cola de respuesta específica, usarla
				queueOut := config.Config.QueueOut
				if result.ReplyQueue != "" {
					queueOut = result.ReplyQueue
					p.log.WithFields(logrus.Fields{
						"message_id": result.MessageID,
						"reply_queue": result.ReplyQueue,
					}).Debug("Usando cola de respuesta específica")
				}
				
				err := p.publishResponse(response, correlationID, queueOut)
				if err != nil {
					p.log.WithError(err).
						WithField("message_id", result.MessageID).
						Error("Error al publicar respuesta en RabbitMQ")
				} else {
					p.log.WithFields(logrus.Fields{
						"message_id": result.MessageID,
						"queue": queueOut,
					}).Debug("Respuesta enviada correctamente")
				}
			} else {
				p.log.WithField("message_id", result.MessageID).
					Error("No se puede enviar respuesta, no hay ID para correlacion")
			}
		}
	}
}

// publishResponse publica la respuesta en RabbitMQ
func (p *ImageProcessor) publishResponse(response Response, correlationID string, queueOut string) error {
	// Si la cola de salida no está especificada, usar la predeterminada
	if queueOut == "" {
		queueOut = config.Config.QueueOut
	}
	
	// Convertir a JSON
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("error al serializar respuesta: %v", err)
	}

	// Verificar que la conexión esté disponible
	if p.connection == nil || !p.connection.IsConnected() {
		p.log.Info("Conexion no disponible, intentando reconectar para enviar respuesta")
		p.connection = rabbitmq.NewRabbitMQConnection(config.Config.GetRabbitMQURLs()[0])
		err = p.connection.Connect()
		if err != nil {
			return fmt.Errorf("error al reconectar para enviar respuesta: %v", err)
		}
	}

	// Enviar respuesta
	err = p.connection.PublishMessage(
		queueOut,
		responseJSON,
		correlationID,
	)

	if err != nil {
		return fmt.Errorf("error al publicar mensaje: %v", err)
	}
	
	p.log.WithFields(logrus.Fields{
		"queue": queueOut,
		"size": len(responseJSON),
		"correlation_id": correlationID,
	}).Debug("Mensaje publicado en cola de respuestas")
	
	return nil
}

// ProcessMessage procesa un mensaje de RabbitMQ
func (p *ImageProcessor) ProcessMessage(body []byte, delivery amqp.Delivery) error {
	// Registrar información detallada sobre el mensaje recibido
	p.log.WithFields(logrus.Fields{
		"msg_size": len(body),
		"correlation_id": delivery.CorrelationId,
		"reply_to": delivery.ReplyTo,
		"message_id": delivery.MessageId,
	}).Info("Mensaje recibido para procesamiento")
	
	startTime := time.Now()

	// Intentar parsear como formato NestJS primero
	var nestMessage struct {
		Pattern string          `json:"pattern"`
		Data    json.RawMessage `json:"data"`
	}

	var messageData []byte
	err := json.Unmarshal(body, &nestMessage)

	if err == nil && nestMessage.Pattern == "images-to-process" && len(nestMessage.Data) > 0 {
		p.log.Debug("Mensaje en formato NestJS recibido")
		messageData = nestMessage.Data
	} else {
		p.log.Debug("Mensaje en formato directo recibido")
		messageData = body
	}

	// Ahora parsear el mensaje real
	var message Message
	err = json.Unmarshal(messageData, &message)
	if err != nil {
		p.log.WithError(err).
			WithField("data_preview", string(messageData[:min(200, len(messageData))])).
			Error("Error al decodificar mensaje JSON")
		return err
	}

	// Verificar si es un mensaje de limpieza
	if message.Command == "cleanup" {
		p.log.Info("Recibido mensaje de limpieza, ignorando procesamiento")
		return nil
	}

	// Extraer información del mensaje
	messageID := message.ID
	if messageID == "" {
		messageID = fmt.Sprintf("unknown-%d", time.Now().Unix())
	}

	filename := message.Filename
	if filename == "" {
		filename = fmt.Sprintf("image-%d.jpg", time.Now().Unix())
	}

	// Obtener datos de la imagen
	imageData, err := p.extractImageData(message)
	if err != nil {
		p.log.WithError(err).
			WithField("message_id", messageID).
			Error("Error al extraer datos de imagen")
		
		// Determinar el correlationID a usar
		correlationID := delivery.CorrelationId
		if correlationID == "" {
			correlationID = messageID
			p.log.WithField("message_id", messageID).
				Warn("CorrelationID no disponible, usando MessageID para respuesta de error")
		}
		
		p.sendErrorResponse(messageID, filename, fmt.Sprintf("Error al extraer datos de imagen: %v", err), correlationID)
		return err
	}

	// Extraer información adicional
	companyID := message.CompanyID
	if companyID == "" {
		companyID = "default"
	}

	userID := message.UserID
	if userID == "" {
		userID = "anonymous"
	}

	module := message.Module
	if module == "" {
		module = "general"
	}

	// Obtener configuración del preset	
	// presetName := message.Preset
	var presetName string = "default"
	var maxWidth, maxHeight, quality int
	var format string

	if message.Options != nil {
		// Intentar convertir a mapa
		if optionsMap, ok := message.Options.(map[string]interface{}); ok {
			// Extraer valores
			if preset, ok := optionsMap["imagePreset"].(string); ok && preset != "" {
				presetName = preset
			}
			if width, ok := optionsMap["maxWidth"].(float64); ok {
				maxWidth = int(width)
			}
			if height, ok := optionsMap["maxHeight"].(float64); ok {
				maxHeight = int(height)
			}
			if q, ok := optionsMap["quality"].(float64); ok {
				quality = int(q)
			}
			if fmt, ok := optionsMap["format"].(string); ok {
				format = fmt
			}
		}
	}

	if message.Preset != "" {
		presetName = message.Preset
	}
	if message.MaxWidth > 0 {
		maxWidth = message.MaxWidth
	}
	if message.MaxHeight > 0 {
		maxHeight = message.MaxHeight
	}
	if message.Quality > 0 {
		quality = message.Quality
	}
	if message.Format != "" {
		format = message.Format
	}




	// if presetName == "" {
	// 	presetName = "default"
	// }

	// Obtener preset
	// options := map[string]interface{}{
	// 	"imagePreset": presetName,
	// 	"maxWidth":    message.MaxWidth,
	// 	"maxHeight":   message.MaxHeight,
	// 	"quality":     message.Quality,
	// 	"format":      message.Format,
	// }
	options := map[string]interface{}{
		"imagePreset": presetName,
		"maxWidth":    maxWidth,
		"maxHeight":   maxHeight,
		"quality":     quality,
		"format":      format,
	}

	preset := p.getPresetFromOptions(options)

	// Determinar tipo MIME/formato
	mimetype := message.Mimetype
	if mimetype == "" {
		mimetype = message.Type
	}
	if mimetype == "" {
		mimetype = p.guessMimetype(filename)
	}

	// Verificar si la imagen necesita procesamiento
	needsProcessing := p.shouldProcessImage(imageData, filename, preset)
	
	// Determinar el correlationID a usar
	correlationID := delivery.CorrelationId
	if correlationID == "" {
		correlationID = messageID
		p.log.WithField("message_id", messageID).
			Warn("CorrelationID no disponible en el mensaje, usando MessageID como alternativa")
	}
	
	// Si la imagen no necesita procesamiento, podemos responder directamente
	if !needsProcessing {
		p.log.WithField("message_id", messageID).
			Info("Imagen no requiere procesamiento, respondiendo directamente")
			
		processingTime := time.Since(startTime).Milliseconds()
		
		response := Response{
			ID:            messageID,
			Processed:     false,
			Filename:      filename,
			CompanyID:     companyID,
			UserID:        userID,
			Module:        module,
			OriginalSize:  len(imageData),
			ProcessedSize: len(imageData),
			Reduction:     "0%",
			Info: map[string]interface{}{
				"processor": "none",
				"reason":    "image already optimal",
			},
			Duration: fmt.Sprintf("%.2fms", float64(processingTime)),
			Success:  true,
		}
		
		// Determinar la cola de respuesta
		queueOut := config.Config.QueueOut
		if message.ReplyQueue != "" {
			queueOut = message.ReplyQueue
		} else if delivery.ReplyTo != "" {
			queueOut = delivery.ReplyTo
		}
		
		// Usar solo el correlation ID para responder
		err = p.publishResponse(response, correlationID, queueOut)
		if err != nil {
			p.log.WithError(err).Error("Error al publicar respuesta directa")
		}
		return nil
	}
	
	// Crear trabajo y enviarlo al worker pool
	job := ImageJob{
		MessageID:     messageID,
		CorrelationID: correlationID,
		Filename:      filename,
		ImageData:     imageData,
		Mimetype:      mimetype,
		Preset:        preset,
		CompanyID:     companyID,
		UserID:        userID,
		Module:        module,
		OriginalSize:  len(imageData),
		ReplyQueue:    message.ReplyQueue,
	}
	
	// Si hay una cola de respuesta en el delivery, usarla
	if delivery.ReplyTo != "" && job.ReplyQueue == "" {
		job.ReplyQueue = delivery.ReplyTo
	}
	
	// Encolar el trabajo
	p.log.WithFields(logrus.Fields{
		"message_id": messageID,
		"correlation_id": correlationID,
	}).Info("Encolando trabajo para procesamiento")
		
	err = p.workerPool.EnqueueJob(job)
	if err != nil {
		p.log.WithError(err).
			WithField("message_id", messageID).
			Error("Error al encolar trabajo")
		p.sendErrorResponse(messageID, filename, fmt.Sprintf("Error al encolar trabajo: %v", err), correlationID)
		return err
	}
	
	p.log.WithField("message_id", messageID).
		Debug("Trabajo encolado correctamente")
	
	return nil

}

// extractImageData extrae los datos de imagen del mensaje
func (p *ImageProcessor) extractImageData(message Message) ([]byte, error) {
	// Buscar los datos de la imagen en diferentes campos posibles
	var imageData interface{}
	if message.Buffer != nil {
		imageData = message.Buffer
	} else if message.Data != nil {
		imageData = message.Data
	} else if message.Content != nil {
		imageData = message.Content
	}

	if imageData == nil {
		return nil, fmt.Errorf("no se encontraron datos de imagen en el mensaje")
	}

	// Convertir a bytes según el tipo
	switch data := imageData.(type) {
	case string:
		// Si es base64, decodificar
		if strings.Contains(data, ",") {
			parts := strings.SplitN(data, ",", 2)
			data = parts[1]
		}
		return base64.StdEncoding.DecodeString(data)
	case map[string]interface{}:
		// Si es un objeto con campo "data"
		if encodedData, ok := data["data"].(string); ok {
			return base64.StdEncoding.DecodeString(encodedData)
		}
		return nil, fmt.Errorf("formato de datos no reconocido")
	case []byte:
		// Ya está en formato binario
		return data, nil
	default:
		return nil, fmt.Errorf("formato de datos no soportado")
	}
}

// getPresetFromOptions obtiene preset basado en opciones
func (p *ImageProcessor) getPresetFromOptions(options map[string]interface{}) config.ImagePreset {
	// Verificar si se especifica un preset
	presetName, _ := options["imagePreset"].(string)
	if presetName == "" {
		presetName = "default"
	}

	preset := config.Config.ImagePresets[presetName]
	if preset.MaxWidth == 0 {
		// Si no existe el preset, usar el default
		preset = config.Config.ImagePresets["default"]
	}

	// Sobrescribir con opciones personalizadas si se proporcionan
	if width, ok := options["maxWidth"].(int); ok && width > 0 {
		preset.MaxWidth = width
	}
	if height, ok := options["maxHeight"].(int); ok && height > 0 {
		preset.MaxHeight = height
	}
	if quality, ok := options["quality"].(int); ok && quality > 0 {
		preset.Quality = quality
	}
	if format, ok := options["format"].(string); ok && format != "" {
		switch strings.ToLower(format) {
		case "jpeg", "jpg":
			preset.Format = config.FormatJPEG
		case "png":
			preset.Format = config.FormatPNG
		case "gif":
			preset.Format = config.FormatGIF
		}
	}

	return preset
}

// guessMimetype determina el tipo MIME basado en la extensión
func (p *ImageProcessor) guessMimetype(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))

	mimeMap := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
	}

	mime, ok := mimeMap[ext]
	if !ok {
		return "image/jpeg" // Valor predeterminado
	}
	return mime
}

// shouldProcessImage determina si la imagen necesita ser procesada
func (p *ImageProcessor) shouldProcessImage(imageData []byte, _ string, preset config.ImagePreset) bool {
	// Verificar el tamaño
	currentSize := len(imageData)
	if int64(currentSize) <= config.Config.ImageSizeThreshold {
		// La imagen ya es pequeña, verificar el formato y dimensiones
		img, format, err := image.Decode(bytes.NewReader(imageData))
		if err != nil {
			// En caso de error, procesar para estar seguros
			return true
		}

		// Obtener dimensiones originales
		bounds := img.Bounds()
		width := bounds.Dx()
		height := bounds.Dy()

		// Si ya está en el formato deseado y es más pequeña que el límite
		if (strings.ToLower(format) == string(preset.Format) ||
			(strings.ToLower(format) == "jpeg" && preset.Format == config.FormatJPEG)) &&
			width <= preset.MaxWidth &&
			height <= preset.MaxHeight {
			return false
		}
	}

	// En otros casos, procesar la imagen
	return true
}

// processImage procesa una imagen según el formato y preset
func (p *ImageProcessor) processImage(imageData []byte, mimetype string, preset config.ImagePreset) ([]byte, map[string]interface{}, error) {
	// Si el formato solicitado no es soportado, usar JPEG como alternativa
	if preset.Format != config.FormatJPEG && preset.Format != config.FormatPNG && preset.Format != config.FormatGIF {
		p.log.Warnf("Formato %s solicitado pero no soportado, usando JPEG como alternativa", preset.Format)
		preset.Format = config.FormatJPEG
	}

	switch preset.Format {
	case config.FormatJPEG:
		return p.processToJPEG(imageData, preset)
	case config.FormatPNG:
		return p.processToPNG(imageData, preset)
	case config.FormatGIF:
		// Implementar procesamiento GIF o usar fallback
		p.log.Warn("Formato GIF solicitado pero no implementado, usando JPEG como alternativa")
		return p.processToJPEG(imageData, preset)
	default:
		// Formato no soportado específicamente, usar JPEG
		return p.processToJPEG(imageData, preset)
	}
}

// ProcessImageWithCtx versión con soporte de contexto para cancelación
// Esta es la función que será llamada desde el worker pool
func (p *ImageProcessor) ProcessImageWithCtx(ctx context.Context, imageData []byte, mimetype string, preset config.ImagePreset) ([]byte, map[string]interface{}, error) {
	// Verificar si el contexto ha sido cancelado antes de iniciar
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
		// Continuar con el procesamiento
	}
	
	// Procesar con verificaciones periódicas del contexto
	doneCh := make(chan struct{})
	resultCh := make(chan struct {
		data []byte
		info map[string]interface{}
		err  error
	})
	
	go func() {
		defer close(doneCh)
		
		data, info, err := p.processImage(imageData, mimetype, preset)
		select {
		case <-ctx.Done():
			// Contexto cancelado, abandonar resultado
			return
		case resultCh <- struct {
			data []byte
			info map[string]interface{}
			err  error
		}{data, info, err}:
			// Resultado enviado correctamente
		}
	}()
	
	// Esperar resultado o cancelación
	select {
	case <-ctx.Done():
		<-doneCh // Esperar a que la goroutine termine
		return nil, nil, ctx.Err()
	case result := <-resultCh:
		return result.data, result.info, result.err
	}
}

// processToJPEG convierte la imagen a formato JPEG
func (p *ImageProcessor) processToJPEG(imageData []byte, preset config.ImagePreset) ([]byte, map[string]interface{}, error) {
	// Decodificar imagen
	img, format, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, nil, fmt.Errorf("error al decodificar imagen: %v", err)
	}

	// Obtener dimensiones originales
	bounds := img.Bounds()
	originalWidth := bounds.Dx()
	originalHeight := bounds.Dy()

	// Redimensionar si es necesario
	var resizedImg image.Image
	if originalWidth > preset.MaxWidth || originalHeight > preset.MaxHeight {
		if preset.PreserveAspectRatio {
			// Mantener proporción de aspecto
			resizedImg = imaging.Fit(img, preset.MaxWidth, preset.MaxHeight, imaging.Lanczos)
		} else {
			// Redimensionar sin mantener proporción
			resizedImg = imaging.Resize(img, preset.MaxWidth, preset.MaxHeight, imaging.Lanczos)
		}
	} else {
		resizedImg = img
	}

	// Convertir transparencia a fondo blanco para JPEG
	if format == "png" {
		// Crear imagen RGB con fondo blanco
		draw := imaging.New(resizedImg.Bounds().Dx(), resizedImg.Bounds().Dy(), image.White)
		draw = imaging.Paste(draw, resizedImg, image.Point{0, 0})
		resizedImg = draw
	}

	// Guardar como JPEG
	buf := new(bytes.Buffer)
	err = jpeg.Encode(buf, resizedImg, &jpeg.Options{
		Quality: preset.Quality,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error al codificar JPEG: %v", err)
	}

	// Información del procesamiento
	info := map[string]interface{}{
		"processor":      "golang-imaging",
		"originalFormat": format,
		"newFormat":      "jpeg",
		"originalWidth":  originalWidth,
		"originalHeight": originalHeight,
		"newWidth":       resizedImg.Bounds().Dx(),
		"newHeight":      resizedImg.Bounds().Dy(),
		"quality":        preset.Quality,
	}

	return buf.Bytes(), info, nil
}

// processToPNG convierte la imagen a formato PNG
func (p *ImageProcessor) processToPNG(imageData []byte, preset config.ImagePreset) ([]byte, map[string]interface{}, error) {
	// Decodificar imagen
	img, format, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, nil, fmt.Errorf("error al decodificar imagen: %v", err)
	}

	// Obtener dimensiones originales
	bounds := img.Bounds()
	originalWidth := bounds.Dx()
	originalHeight := bounds.Dy()

	// Redimensionar si es necesario
	var resizedImg image.Image
	if originalWidth > preset.MaxWidth || originalHeight > preset.MaxHeight {
		if preset.PreserveAspectRatio {
			// Mantener proporción de aspecto
			resizedImg = imaging.Fit(img, preset.MaxWidth, preset.MaxHeight, imaging.Lanczos)
		} else {
			// Redimensionar sin mantener proporción
			resizedImg = imaging.Resize(img, preset.MaxWidth, preset.MaxHeight, imaging.Lanczos)
		}
	} else {
		resizedImg = img
	}

	// Guardar como PNG
	buf := new(bytes.Buffer)
	err = png.Encode(buf, resizedImg)
	if err != nil {
		return nil, nil, fmt.Errorf("error al codificar PNG: %v", err)
	}

	// Información del procesamiento
	info := map[string]interface{}{
		"processor":      "golang-imaging",
		"originalFormat": format,
		"newFormat":      "png",
		"originalWidth":  originalWidth,
		"originalHeight": originalHeight,
		"newWidth":       resizedImg.Bounds().Dx(),
		"newHeight":      resizedImg.Bounds().Dy(),
		"quality":        "lossless", // PNG es siempre lossless
	}

	return buf.Bytes(), info, nil
}

// sendErrorResponse envía respuesta de error
func (p *ImageProcessor) sendErrorResponse(messageID, filename, errorMessage string, correlationID string) {
	errorResponse := Response{
		ID:        messageID,
		Processed: false,
		Filename:  filename,
		Error:     errorMessage,
		Success:   false,
	}

	err := p.publishResponse(errorResponse, correlationID, "")
	if err != nil {
		p.log.WithError(err).Error("Error al enviar respuesta de error")
	}
}