package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"


	"image-processing-microservice-go/internal/config"
	"image-processing-microservice-go/internal/logger"
	"image-processing-microservice-go/internal/processor"
	"image-processing-microservice-go/internal/rabbitmq"
	processor2 "image-processing-microservice-go/metrics"

)

func main() {
	processor2.InitMetricsServer("8080")
	// Definir flags de línea de comandos
	envFile := flag.String("env", "", "Ruta al archivo .env (opcional)")
	debugMode := flag.Bool("debug", false, "Activar modo debug")
	showVersion := flag.Bool("version", false, "Mostrar versión del servicio")
	flag.Parse()

	// Mostrar versión si se solicita
	if *showVersion {
		fmt.Println("Servicio de procesamiento de imagenes v1.0.0")
		os.Exit(0)
	}

	// Si se especificó un archivo .env por línea de comandos
	if *envFile != "" {
		os.Setenv("ENV_FILE", *envFile)
	}

	// Si se especificó modo debug
	if *debugMode {
		os.Setenv("DEBUG", "true")
	}

	// Cargar configuración primero
	config.LoadConfig()

	// Inicializar logger después de cargar configuración
	logger.InitLogger(config.Config.LogLevel, config.Config.LogToFile, config.Config.LogDir)
	log := logger.GetLogger("main")

	// Capturar panics
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC CRÍTICO DETECTADO: %v", r)
			debug.PrintStack()
			os.Exit(1)
		}
	}()

	log.Println("============= SERVICIO DE PROCESAMIENTO DE IMAGENES =============")
	log.Println("Iniciando servicio de procesamiento de imagenes (Go)")

	// Crear el archivo PID para monitoreo
	writePIDFile()

	// Crear procesador de imágenes
	log.Println("Creando procesador de imagenes...")
	imageProcessor := processor.NewImageProcessor(nil)

	// Crear consumidor
	log.Println("Creando consumidor...")
	consumer := rabbitmq.NewConsumer(imageProcessor.ProcessMessage)

	// Iniciar consumidor
	log.Println("Iniciando consumidor...")
	err := consumer.Start()
	if err != nil {
		log.Fatalf("Error al iniciar consumidor: %v", err)
	}



	// Enviar mensaje de prueba en modo debug
	if config.Config.Debug {
		time.Sleep(1 * time.Second) // Dar tiempo a que el consumidor inicie
		log.Println("Enviando mensaje de prueba...")
		testConnection := rabbitmq.NewRabbitMQConnection(config.Config.GetRabbitMQURLs()[0])
		err := testConnection.Connect()
		if err == nil {
			testData := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII="
			testMsg := []byte(`{"id":"test-123","filename":"test.jpg","data":"` + testData + `","mimetype":"image/jpeg","preset":"default"}`)
			err = testConnection.PublishMessage(config.Config.QueueIn, testMsg, "test-correlation")
			if err != nil {
				log.Printf("Error al enviar mensaje de prueba: %v", err)
			} else {
				log.Println("Mensaje de prueba enviado correctamente")
			}
			testConnection.Close()
		} else {
			log.Printf("Error al conectar para mensaje de prueba: %v", err)
		}
	}

	log.Println("Servicio inicializado correctamente")
	log.Println("Esperando mensajes...")

	

	// Manejar señales de terminación
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)

	// Esperar señal
	<-stopChan
	log.Println("Senial de terminacion recibida. Iniciando apagado ordenado...")


	// Detener procesador de imágenes
	log.Println("Deteniendo procesador de imagenes...")
	imageProcessor.Stop()

	// Detener consumidor
	err = consumer.Stop()
	if err != nil {
		log.Printf("Error al detener consumidor: %v", err)
	} else {
		log.Println("Consumidor detenido correctamente")
	}

	log.Println("Servicio finalizado")
}

func writePIDFile() {
	pid := os.Getpid()
	file, err := os.Create("processor.pid")
	if err != nil {
		log.Printf("Error al crear archivo PID: %v", err)
		return
	}
	defer file.Close()

	_, err = file.WriteString(strconv.Itoa(pid))
	if err != nil {
		log.Printf("Error al escribir PID: %v", err)
		return
	}

	log.Printf("PID %d guardado en processor.pid", pid)
}