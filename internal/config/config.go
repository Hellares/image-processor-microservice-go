package config

import (
	// "bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// ImageFormat representa los formatos de imagen soportados
type ImageFormat string

const (
	FormatJPEG ImageFormat = "jpeg"
	FormatPNG  ImageFormat = "png"
	// FormatWEBP ImageFormat = "webp"
	FormatGIF  ImageFormat = "gif"
)

// ImagePreset representa configuraciones predefinidas para diferentes tipos de imágenes
type ImagePreset struct {
	MaxWidth           int
	MaxHeight          int
	Quality            int
	Format             ImageFormat
	PreserveAspectRatio bool
}

// Settings almacena toda la configuración del servicio
type Settings struct {
	// RabbitMQ
	RabbitMQServers                  string
	QueueIn                          string
	QueueOut                         string
	QueueDLX                         string
	QueueDurable                     bool
	PrefetchCount                    int
	MaxWorkers                       int
	ImageSizeThreshold               int64
	ProcessingTimeout                int
	RabbitMQHeartbeat                int
	RabbitMQBlockedConnectionTimeout int
	RabbitMQConnectionTimeout        int
	RabbitMQRetryDelay               int
	Debug                            bool
	// Logging
	LogLevel  						string
	LogToFile 						bool
	LogDir    						string

	// Presets predefinidos
	ImagePresets map[string]ImagePreset
}

// Config es la instancia global de configuración
var Config Settings

// Busca y carga el archivo .env
func findAndLoadEnvFile() bool {
	// Obtenemos el directorio de ejecución
	execDir, err := os.Getwd()
	if err != nil {
		log.Printf("Error al obtener directorio actual: %v", err)
		execDir = "."
	}

	// Obtenemos el directorio del binario
	var binaryDir string
	if exePath, err := os.Executable(); err == nil {
		binaryDir = filepath.Dir(exePath)
	} else {
		binaryDir = execDir
	}

	// Intenta obtener el directorio del código fuente (funciona en desarrollo)
	_, filename, _, ok := runtime.Caller(0)
	var sourceDir string
	if ok {
		sourceDir = filepath.Dir(filepath.Dir(filename)) // sube dos niveles (asumiendo internal/config)
	} else {
		sourceDir = execDir
	}

	// Lista de posibles ubicaciones para el archivo .env
	possibleLocations := []string{
		filepath.Join(execDir, ".env"),
		filepath.Join(execDir, "../.env"),
		filepath.Join(execDir, "../../.env"),
		filepath.Join(binaryDir, ".env"),
		filepath.Join(binaryDir, "../.env"),
		filepath.Join(sourceDir, ".env"),
	}

	// Buscar en ubicaciones específicas según la estructura del proyecto
	projectDirs := []string{"config", "configs", "etc", "deploy", "env"}
	for _, dir := range projectDirs {
		possibleLocations = append(possibleLocations, filepath.Join(execDir, dir, ".env"))
		possibleLocations = append(possibleLocations, filepath.Join(binaryDir, dir, ".env"))
		possibleLocations = append(possibleLocations, filepath.Join(sourceDir, dir, ".env"))
	}

	// Buscar archivo .env en todas las posibles ubicaciones
	for _, envPath := range possibleLocations {
		if _, err := os.Stat(envPath); err == nil {
			err := godotenv.Load(envPath)
			if err == nil {
				log.Printf("Archivo .env cargado correctamente desde: %s", envPath)
				return true
			} else {
				log.Printf("Error al cargar .env desde %s: %v", envPath, err)
			}
		}
	}

	// Mostrar mensaje de depuración
	log.Println("Rutas verificadas para el archivo .env:")
	for i, path := range possibleLocations {
		log.Printf("[%d] %s", i+1, path)
	}

	return false
}

// Crea un archivo .env con valores predeterminados
func createDefaultEnvFile() error {
	// Contenido predeterminado basado en las variables vistas en tu .env original
	defaultEnvContent := `RABBITMQ_SERVERS="amqp://jtorres:jtorres159.@161.132.50.183:5672"

# Colas
QUEUE_IN=images-to-process
QUEUE_OUT=processed-images
QUEUE_DLX=images-process-failed

# Opciones de cola
QUEUE_DURABLE=true
PREFETCH_COUNT=5

# Procesamiento
MAX_WORKERS=6
IMAGE_SIZE_THRESHOLD=1048576
PROCESSING_TIMEOUT=30

# Conexión RabbitMQ
RABBITMQ_HEARTBEAT=120
RABBITMQ_BLOCKED_CONNECTION_TIMEOUT=300
RABBITMQ_CONNECTION_TIMEOUT=20000
RABBITMQ_RETRY_DELAY=5

# Debug
DEBUG=true`

	// Ruta para el nuevo archivo
	dirActual, err := os.Getwd()
	if err != nil {
		return err
	}

	envPath := filepath.Join(dirActual, ".env")
	
	// Verificar si el archivo ya existe
	if _, err := os.Stat(envPath); err == nil {
		log.Printf("El archivo %s ya existe, no se sobrescribirá", envPath)
		return nil
	}

	// Crear el archivo
	err = os.WriteFile(envPath, []byte(defaultEnvContent), 0644)
	if err != nil {
		return fmt.Errorf("error al crear archivo .env predeterminado: %v", err)
	}

	log.Printf("Archivo .env predeterminado creado en: %s", envPath)
	
	// Cargar el archivo recién creado
	return godotenv.Load(envPath)
}



// LoadConfig carga la configuración desde variables de entorno o archivo .env
func LoadConfig() {

	log.Println("Cargando configuración del sistema...")

	// Intentar cargar desde variable de entorno explícita primero
	envFilePath := os.Getenv("ENV_FILE")
	if envFilePath != "" {
		err := godotenv.Load(envFilePath)
		if err == nil {
			log.Printf("Archivo .env cargado desde ENV_FILE: %s", envFilePath)
		} else {
			log.Printf("Error al cargar .env desde ENV_FILE (%s): %v", envFilePath, err)
		}
	} else {
		// Buscar y cargar el archivo .env automáticamente
		envFound := findAndLoadEnvFile()
		
		// Si no se encontró el archivo, intentar crearlo
		if !envFound {
			log.Println("No se encontró archivo .env. Creando uno con valores por defecto...")
			err := createDefaultEnvFile()
			if err != nil {
				log.Printf("Error al crear archivo .env predeterminado: %v", err)
				log.Println("Continuando con valores predeterminados en memoria...")
			} else {
				log.Println("Archivo .env creado y cargado correctamente")
			}
		}
	}
	


	// Configuración principal
	Config = Settings{
		RabbitMQServers:                  getEnv("RABBITMQ_SERVERS", "amqp://guest:guest@localhost:5672"),
		QueueIn:                          getEnv("QUEUE_IN", "images-to-process"),
		QueueOut:                         getEnv("QUEUE_OUT", "processed-images"),
		QueueDLX:                         getEnv("QUEUE_DLX", "images-process-failed"),
		QueueDurable:                     getEnvBool("QUEUE_DURABLE", true),
		PrefetchCount:                    getEnvInt("PREFETCH_COUNT", 5),
		MaxWorkers:                       getEnvInt("MAX_WORKERS", 6),
		ImageSizeThreshold:               int64(getEnvInt("IMAGE_SIZE_THRESHOLD", 1048576)),
		ProcessingTimeout:                getEnvInt("PROCESSING_TIMEOUT", 30),
		RabbitMQHeartbeat:                getEnvInt("RABBITMQ_HEARTBEAT", 120),
		RabbitMQBlockedConnectionTimeout: getEnvInt("RABBITMQ_BLOCKED_CONNECTION_TIMEOUT", 300),
		RabbitMQConnectionTimeout:        getEnvInt("RABBITMQ_CONNECTION_TIMEOUT", 20000),
		RabbitMQRetryDelay:               getEnvInt("RABBITMQ_RETRY_DELAY", 5),
		Debug:                            getEnvBool("DEBUG", true),
		LogLevel:                         getEnv("LOG_LEVEL", "info"),
		LogToFile:                        getEnvBool("LOG_TO_FILE", false),	
		LogDir:                          getEnv("LOG_DIR", "./logs"),
	}

	// Configurar los presets de imágenes
	Config.ImagePresets = map[string]ImagePreset{
		"profile": {
			MaxWidth:           500,
			MaxHeight:          500,
			Quality:            85,
			Format:             FormatJPEG,
			PreserveAspectRatio: true,
		},
		"servicio": {
			MaxWidth:           720,
			MaxHeight:          480,
			Quality:            85,
			Format:             FormatJPEG,
			PreserveAspectRatio: false,
		},
		"producto": {
			MaxWidth:           1200,
			MaxHeight:          1200,
			Quality:            85,
			Format:             FormatJPEG,
			PreserveAspectRatio: true,
		},
		"banner": {
			MaxWidth:           1280,
			MaxHeight:          720,
			Quality:            80,
			Format:             FormatJPEG,
			PreserveAspectRatio: true,
		},
		"thumbnail": {
			MaxWidth:           300,
			MaxHeight:          300,
			Quality:            75,
			Format:             FormatJPEG,
			PreserveAspectRatio: true,
		},
		"default": {
			MaxWidth:           1600,
			MaxHeight:          1600,
			Quality:            80,
			Format:             FormatJPEG,
			PreserveAspectRatio: true,
		},
	}

	// Configurar nivel de log
	if Config.Debug {
        log.Println("Modo de depuracion activado")
    }

	// Imprimir configuración cargada para debug
	log.Println("Configuración cargada correctamente:")
	log.Printf("- RabbitMQ URL: %s", Config.RabbitMQServers)
	log.Printf("- Cola de entrada: %s", Config.QueueIn)
	log.Printf("- Cola de salida: %s", Config.QueueOut)
	log.Printf("- Trabajadores máximos: %d", Config.MaxWorkers)
	log.Printf("- Modo Debug: %v", Config.Debug)
}

// GetRabbitMQURLs obtiene las URLs de RabbitMQ
func (s *Settings) GetRabbitMQURLs() []string {
	servers := s.RabbitMQServers
	// Eliminar comillas si están presentes
	servers = strings.Trim(servers, "\"")

	// Dividir en caso de múltiples URLs
	if strings.Contains(servers, ",") {
		return strings.Split(servers, ",")
	}
	return []string{servers}
}

// GetRabbitMQConnectionParams obtiene parámetros de conexión para RabbitMQ
func (s *Settings) GetRabbitMQConnectionParams() map[string]interface{} {
	// En Go, usamos directamente la URL para conectar con RabbitMQ
	return map[string]interface{}{
		"url":     s.GetRabbitMQURLs()[0],
		"timeout": time.Duration(s.RabbitMQConnectionTimeout) * time.Millisecond,
	}
}

// Funciones auxiliares para obtener variables de entorno

func getEnv(key, defaultValue string) string {
    value := os.Getenv(key)
    
    // log.Printf("Buscando variable '%s'. Valor encontrado: '%s'", key, value)
    
    if value == "" {
        // log.Printf("Usando valor por defecto para '%s': '%s'", key, defaultValue)
        return defaultValue
    }
    
    // Limpiar comillas si están presentes
    value = strings.Trim(value, "\"")
    return value
}

func getEnvInt(key string, defaultValue int) int {
	strValue := getEnv(key, "")
	if strValue == "" {
		return defaultValue
	}

	// Eliminar cualquier comentario si existe
	if idx := strings.Index(strValue, "#"); idx != -1 {
		strValue = strValue[:idx]
	}

	value, err := strconv.Atoi(strings.TrimSpace(strValue))
	if err != nil {
		log.Printf("Error convirtiendo %s a entero: %v, usando valor predeterminado %d", key, err, defaultValue)
		return defaultValue
	}
	return value
}

func getEnvBool(key string, defaultValue bool) bool {
	strValue := strings.ToLower(getEnv(key, ""))
	if strValue == "" {
		return defaultValue
	}

	switch strValue {
	case "true", "t", "1", "yes", "y":
		return true
	case "false", "f", "0", "no", "n":
		return false
	default:
		return defaultValue
	}
}