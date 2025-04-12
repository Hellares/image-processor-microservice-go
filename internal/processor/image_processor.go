package processor

import (
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
	"image-processing-microservice-go/internal/rabbitmq"

	"github.com/disintegration/imaging"
	amqp "github.com/rabbitmq/amqp091-go"
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
}

// Message representa la estructura de los mensajes de entrada
type Message struct {
	ID        string      `json:"id"`
	Filename  string      `json:"filename"`
	Data      interface{} `json:"data,omitempty"`
	Buffer    interface{} `json:"buffer,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	CompanyID string      `json:"companyId,omitempty"`
	UserID    string      `json:"userId,omitempty"`
	Module    string      `json:"module,omitempty"`
	Preset    string      `json:"preset,omitempty"`
	MaxWidth  int         `json:"maxWidth,omitempty"`
	MaxHeight int         `json:"maxHeight,omitempty"`
	Quality   int         `json:"quality,omitempty"`
	Format    string      `json:"format,omitempty"`
	Mimetype  string      `json:"mimetype,omitempty"`
	Type      string      `json:"type,omitempty"`
	Command   string      `json:"command,omitempty"`
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

	return &ImageProcessor{
		connection: connection,
	}
}

// ProcessMessage procesa un mensaje de RabbitMQ
func (p *ImageProcessor) ProcessMessage(body []byte, delivery amqp.Delivery) error {
	log.Printf("Mensaje recibido en ProcessMessage: %d bytes", len(body))
	startTime := time.Now()

	// Intentar parsear como formato NestJS primero
	var nestMessage struct {
		Pattern string          `json:"pattern"`
		Data    json.RawMessage `json:"data"`
	}

	var messageData []byte
	err := json.Unmarshal(body, &nestMessage)

	if err == nil && nestMessage.Pattern == "images-to-process" && len(nestMessage.Data) > 0 {
		// Es un mensaje en formato NestJS
		log.Println("Mensaje en formato NestJS recibido")
		messageData = nestMessage.Data
	} else {
		// Es un mensaje directo
		log.Println("Mensaje en formato directo recibido")
		messageData = body
	}

	// Ahora parsear el mensaje real
	var message Message
	err = json.Unmarshal(messageData, &message)
	if err != nil {
		log.Printf("Error al decodificar mensaje JSON: %v", err)
		log.Printf("Mensaje que causó el error: %s", string(messageData[:min(200, len(messageData))]))
		return err
	}

	// Verificar si es un mensaje de limpieza
	if message.Command == "cleanup" {
		log.Println("Recibido mensaje de limpieza, ignorando procesamiento de imagen")
		return nil
	}

	// Extraer información del mensaje
	messageID := message.ID
	if messageID == "" {
		messageID = "unknown"
	}

	filename := message.Filename
	if filename == "" {
		filename = "image.jpg"
	}

	log.Printf("Procesando imagen: ID=%s, Archivo=%s", messageID, filename)

	// Obtener datos de la imagen
	imageData, err := p.extractImageData(message)
	if err != nil {
		log.Printf("Error al extraer datos de imagen: %v", err)
		p.sendErrorResponse(messageID, filename, fmt.Sprintf("Error al extraer datos de imagen: %v", err), delivery)
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
	presetName := message.Preset
	if presetName == "" {
		presetName = "default"
	}

	// Obtener preset
	options := map[string]interface{}{
		"imagePreset": presetName,
		"maxWidth":    message.MaxWidth,
		"maxHeight":   message.MaxHeight,
		"quality":     message.Quality,
		"format":      message.Format,
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

	// Determinar si es necesario procesar la imagen
	if !p.shouldProcessImage(imageData, filename, preset) {
		log.Printf("Imagen %s no requiere procesamiento, enviando sin cambios", filename)
		processingTime := time.Since(startTime).Milliseconds()

		// Crear respuesta con la imagen original
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

		// Enviar respuesta
		p.sendResponse(response, delivery)
		return nil
	}

	// Procesar imagen
	processedData, info, err := p.processImage(imageData, mimetype, preset)
	if err != nil {
		log.Printf("Error al procesar imagen: %v", err)
		p.sendErrorResponse(messageID, filename, fmt.Sprintf("Error al procesar imagen: %v", err), delivery)
		return err
	}

	// Calcular estadísticas
	originalSize := len(imageData)
	processedSize := len(processedData)
	var reductionPercent float64
	if originalSize > 0 {
		reductionPercent = float64(originalSize-processedSize) / float64(originalSize) * 100
	}

	// Si la imagen procesada es más grande o igual que la original, usar la original
	if processedSize >= originalSize {
		log.Printf("Imagen %s procesada pero sin mejoras, usando original", filename)
		processingTime := time.Since(startTime).Milliseconds()

		response := Response{
			ID:            messageID,
			Processed:     true,
			Filename:      filename,
			CompanyID:     companyID,
			UserID:        userID,
			Module:        module,
			OriginalSize:  originalSize,
			ProcessedSize: originalSize,
			Reduction:     "0%",
			Info:          info,
			Duration:      fmt.Sprintf("%.2fms", float64(processingTime)),
			Success:       true,
		}

		p.sendResponse(response, delivery)
		return nil
	}

	// Generar respuesta con la imagen procesada
	processingTime := time.Since(startTime).Milliseconds()

	response := Response{
		ID:            messageID,
		Processed:     true,
		Filename:      filename,
		CompanyID:     companyID,
		UserID:        userID,
		Module:        module,
		OriginalSize:  originalSize,
		ProcessedSize: processedSize,
		Reduction:     fmt.Sprintf("%.2f%%", reductionPercent),
		Info:          info,
		Duration:      fmt.Sprintf("%.2fms", float64(processingTime)),
		Success:       true,
	}

	log.Printf("Imagen procesada: %s - Reduccion: %.2f%%, Tamanio: %.2fKB -> %.2fKB, Duracion: %.2fms",
		filename, reductionPercent, float64(originalSize)/1024, float64(processedSize)/1024, float64(processingTime))

		if len(processedData) > 0 {
			// Incluir los datos procesados en la respuesta
			response.ProcessedData = base64.StdEncoding.EncodeToString(processedData)
		}
	// Enviar respuesta
	p.sendResponse(response, delivery)
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
		case "webp":
			preset.Format = config.FormatWEBP
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
	// Si se solicita WebP, usar JPEG como alternativa
	if preset.Format == config.FormatWEBP {
		log.Printf("Formato WebP solicitado pero no soportado, usando JPEG como alternativa")
		preset.Format = config.FormatJPEG
	}

	switch preset.Format {
	case config.FormatJPEG:
		return p.processToJPEG(imageData, preset)
	case config.FormatPNG:
		return p.processToPNG(imageData, preset)
	case config.FormatWEBP:
		return p.processToWEBP(imageData, preset)
	default:
		// Formato no soportado específicamente, usar JPEG
		return p.processToJPEG(imageData, preset)
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
		background := image.NewRGBA(resizedImg.Bounds())
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				background.Set(x, y, image.White)
			}
		}
		// Dibuja la imagen original sobre el fondo blanco
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

// sendResponse envía la respuesta al servicio de origen
func (p *ImageProcessor) sendResponse(response Response, delivery amqp.Delivery) {
	// Convertir a JSON
	responseJSON, err := json.Marshal(response)
	if err != nil {
		log.Printf("Error al serializar respuesta: %v", err)
		return
	}

	// Verificar que la conexión esté disponible
	if p.connection == nil || !p.connection.IsConnected() {
		log.Println("Conexion no disponible, intentando reconectar para enviar respuesta")
		p.connection = rabbitmq.NewRabbitMQConnection(config.Config.GetRabbitMQURLs()[0])
		err = p.connection.Connect()
		if err != nil {
			log.Printf("Error al reconectar para enviar respuesta: %v", err)
			return
		}
	}

	// Enviar respuesta
	correlationID := delivery.CorrelationId
	err = p.connection.PublishMessage(
		config.Config.QueueOut,
		responseJSON,
		correlationID,
	)

	if err != nil {
		log.Printf("Error al enviar respuesta: %v", err)
	} else {
		log.Printf("Respuesta enviada correctamente a cola %s", config.Config.QueueOut)
	}
}

// sendErrorResponse envía respuesta de error
func (p *ImageProcessor) sendErrorResponse(messageID, filename, errorMessage string, delivery amqp.Delivery) {
	errorResponse := Response{
		ID:        messageID,
		Processed: false,
		Filename:  filename,
		Error:     errorMessage,
		Success:   false,
	}

	p.sendResponse(errorResponse, delivery)
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

// processToWEBP convierte la imagen a formato WebP
// processToWEBP convierte la imagen a formato WebP usando kolesa-team/go-webp
func (p *ImageProcessor) processToWEBP(imageData []byte, preset config.ImagePreset) ([]byte, map[string]interface{}, error) {
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

    // Crear un buffer para la salida
    buf := new(bytes.Buffer)

    // Intentar codificar con configuración lossy (con pérdida)
    // err = webp.Encode(buf, resizedImg, &webp.EncoderOptions{
    //     Lossless: false,
    //     Quality:  float32(preset.Quality),
    // })

    // if err != nil {
    //     return nil, nil, fmt.Errorf("error al codificar WebP: %v", err)
    // }

    // Información del procesamiento
    info := map[string]interface{}{
        "processor":      "golang-webp",
        "originalFormat": format,
        "newFormat":      "webp",
        "originalWidth":  originalWidth,
        "originalHeight": originalHeight,
        "newWidth":       resizedImg.Bounds().Dx(),
        "newHeight":      resizedImg.Bounds().Dy(),
        "quality":        preset.Quality,
    }

    return buf.Bytes(), info, nil
}

