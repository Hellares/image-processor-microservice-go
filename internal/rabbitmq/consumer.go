package rabbitmq

import (
	"fmt"
	"log"
	"sync"
	"time"

	"image-processing-microservice-go/internal/config"
	"image-processing-microservice-go/internal/logger"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

// MessageHandler es el tipo para funciones de callback que manejan mensajes
type MessageHandler func([]byte, amqp.Delivery) error

// Consumer gestiona el consumo de mensajes de RabbitMQ
type Consumer struct {
	connection *RabbitMQConnection
	handler    MessageHandler
	running    bool
	mutex      sync.Mutex
	stopChan   chan struct{}
	log *logrus.Entry
}

// NewConsumer crea un nuevo consumidor
func NewConsumer(handler MessageHandler) *Consumer {
	return &Consumer{
		handler:  handler,
		running:  false,
		stopChan: make(chan struct{}),
		log:      logger.Log.WithField("component", "rabbitmq-consumer"),
	}
}

// Start inicia el consumo de mensajes
func (c *Consumer) Start() error {
	c.mutex.Lock()
	if c.running {
		c.mutex.Unlock()
		c.log.Info("Consumidor ya esta en ejecucion")
		return nil
	}
	c.mutex.Unlock()

	// Crear conexión
	c.connection = NewRabbitMQConnection(config.Config.GetRabbitMQURLs()[0])
	err := c.connection.Connect()
	if err != nil {
		return fmt.Errorf("error al conectar a RabbitMQ: %v", err)
	}

	// Suscribirse a la cola
	messages, err := c.connection.Subscribe(config.Config.QueueIn, false)
	if err != nil {
		return fmt.Errorf("error al suscribirse a la cola: %v", err)
	}

	c.mutex.Lock()
	c.running = true
	c.mutex.Unlock()

	// log.Printf("Consumidor iniciado. Esperando mensajes en cola '%s'", config.Config.QueueIn)
	c.log.WithField("queue", config.Config.QueueIn).Info("Consumidor iniciado")

	// Procesar mensajes en una goroutine
	go c.processMessages(messages)

	return nil
}

// processMessages procesa los mensajes recibidos
func (c *Consumer) processMessages(messages <-chan amqp.Delivery) {
	for {
		select {
		case <-c.stopChan:
			log.Println("Recibida senial de detencion, finalizando procesamiento")
			return
		case msg, ok := <-messages:
			if !ok {
				log.Println("Canal de mensajes cerrado, reconectando...")
				// Intentar reconectar
				time.Sleep(5 * time.Second)
				if c.running {
					c.reconnect()
				}
				return
			}

			log.Printf("Mensaje recibido: %d bytes", len(msg.Body))

			// Procesar mensaje
			err := c.handler(msg.Body, msg)
			if err != nil {
				log.Printf("Error procesando mensaje: %v", err)
				msg.Nack(false, false) // Enviar a DLQ
			} else {
				msg.Ack(false)
				log.Println("Mensaje procesado correctamente")
			}
		}
	}
}

// reconnect intenta reconectar si perdimos la conexión
func (c *Consumer) reconnect() {
	if !c.running {
		return
	}

	log.Println("Intentando reconectar...")

	// Cerrar conexión anterior si existe
	if c.connection != nil {
		c.connection.Close()
	}

	// Crear nueva conexión
	c.connection = NewRabbitMQConnection(config.Config.GetRabbitMQURLs()[0])
	err := c.connection.Connect()
	if err != nil {
		log.Printf("Error al reconectar: %v. Reintentando en 5 segundos...", err)
		time.Sleep(5 * time.Second)
		go c.reconnect()
		return
	}

	// Suscribirse nuevamente
	messages, err := c.connection.Subscribe(config.Config.QueueIn, false)
	if err != nil {
		log.Printf("Error al suscribirse: %v. Reintentando en 5 segundos...", err)
		time.Sleep(5 * time.Second)
		go c.reconnect()
		return
	}

	// Procesar mensajes en nueva goroutine
	go c.processMessages(messages)
}

// Stop detiene el consumo de mensajes
func (c *Consumer) Stop() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.running {
		return nil
	}

	log.Println("Deteniendo consumidor...")
	c.running = false

	// Señal para detener el bucle de procesamiento
	close(c.stopChan)

	// Cerrar conexión
	if c.connection != nil {
		c.connection.Close()
	}

	log.Println("Consumidor detenido correctamente")
	return nil
}



