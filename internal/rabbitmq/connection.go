package rabbitmq

import (
	"fmt"
	"log"
	"sync"
	"time"

	myconfig "image-processing-microservice-go/internal/config"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQConnection gestiona la conexión con RabbitMQ
type RabbitMQConnection struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	url        string
	connected  bool
	mutex      sync.Mutex
}

// NewRabbitMQConnection crea una nueva instancia de conexión a RabbitMQ
func NewRabbitMQConnection(url string) *RabbitMQConnection {
	return &RabbitMQConnection{
		url:       url,
		connected: false,
	}
}

// Connect establece la conexión con RabbitMQ
func (r *RabbitMQConnection) Connect() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Si ya estamos conectados, no hacer nada
	if r.connected && r.connection != nil && !r.connection.IsClosed() {
		log.Println("Ya estaba conectado, retornando")
		return nil
	}

	log.Printf("Intentando conectar a URL: %s", r.url)

	// Configurar opciones de conexión
	config := amqp.Config{
		Heartbeat: time.Duration(myconfig.Config.RabbitMQHeartbeat) * time.Second,
		Dial:      amqp.DefaultDial(time.Duration(myconfig.Config.RabbitMQConnectionTimeout) * time.Millisecond),
		Properties: amqp.Table{
			"connection_name": fmt.Sprintf("image-processor-go-%d", time.Now().Unix()),
			"product":         "image-processing-service-go",
			"version":         "1.0",
		},
	}

	// Conectar con RabbitMQ
	var err error
	r.connection, err = amqp.DialConfig(r.url, config)
	if err != nil {
		return fmt.Errorf("error al conectar a RabbitMQ: %v", err)
	}

	// Crear un canal
	r.channel, err = r.connection.Channel()
	if err != nil {
		r.connection.Close()
		return fmt.Errorf("error al crear canal: %v", err)
	}

	// Configurar QoS
	err = r.channel.Qos(myconfig.Config.PrefetchCount, 0, false)
	if err != nil {
		r.channel.Close()
		r.connection.Close()
		return fmt.Errorf("error al configurar QoS: %v", err)
	}

	// Declarar colas
	err = r.declareQueues()
	if err != nil {
		r.channel.Close()
		r.connection.Close()
		return fmt.Errorf("error al declarar colas: %v", err)
	}

	r.connected = true
	log.Println("Conexion establecida con RabbitMQ correctamente")
	return nil
}

// declareQueues declara las colas necesarias
func (r *RabbitMQConnection) declareQueues() error {
	// Cola para mensajes fallidos (dead letter queue)
	_, err := r.channel.QueueDeclare(
		myconfig.Config.QueueDLX,
		myconfig.Config.QueueDurable,
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("error al declarar cola de mensajes fallidos: %v", err)
	}

	// Cola para mensajes entrantes
	_, err = r.channel.QueueDeclare(
		myconfig.Config.QueueIn,
		myconfig.Config.QueueDurable,
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    "",
			"x-dead-letter-routing-key": myconfig.Config.QueueDLX,
		},
	)
	if err != nil {
		return fmt.Errorf("error al declarar cola de entrada: %v", err)
	}

	// Cola para mensajes procesados
	_, err = r.channel.QueueDeclare(
		myconfig.Config.QueueOut,
		myconfig.Config.QueueDurable,
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("error al declarar cola de salida: %v", err)
	}

	log.Println("Todas las colas declaradas correctamente")
	return nil
}

// IsConnected verifica si la conexión está activa
func (r *RabbitMQConnection) IsConnected() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.connected && r.connection != nil && !r.connection.IsClosed() && r.channel != nil && !r.channel.IsClosed()
}

// Close cierra la conexión
func (r *RabbitMQConnection) Close() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Cerrar canal y conexión
	if r.channel != nil {
		r.channel.Close()
		r.channel = nil
	}
	if r.connection != nil {
		r.connection.Close()
		r.connection = nil
	}

	r.connected = false
	log.Println("Conexion con RabbitMQ cerrada correctamente")
}

// PublishMessage publica un mensaje en la cola especificada
func (r *RabbitMQConnection) PublishMessage(queue string, message []byte, correlationID string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Verificar si estamos conectados
	if !r.connected || r.connection == nil || r.connection.IsClosed() || r.channel == nil || r.channel.IsClosed() {
		return fmt.Errorf("no hay conexion disponible para publicar mensaje")
	}

	// Crear propiedades del mensaje
	props := amqp.Publishing{
		DeliveryMode:  amqp.Persistent,
		ContentType:   "application/json",
		CorrelationId: correlationID,
		Timestamp:     time.Now(),
		MessageId:     fmt.Sprintf("%d-%d", time.Now().Unix(), time.Now().UnixNano()%1000000),
		Body:          message,
	}

	// Publicar mensaje
	err := r.channel.Publish(
		"",    // exchange
		queue, // routing key
		false, // mandatory
		false, // immediate
		props,
	)

	if err != nil {
		return fmt.Errorf("error al publicar mensaje: %v", err)
	}

	if myconfig.Config.Debug {
		log.Printf("Mensaje de %d bytes enviado a cola %s", len(message), queue)
	}

	return nil
}

// Subscribe suscribe a una cola para consumir mensajes
func (r *RabbitMQConnection) Subscribe(queue string, autoAck bool) (<-chan amqp.Delivery, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	log.Printf("Solicitando suscripcion a cola '%s', autoAck=%v", queue, autoAck)

	if !r.connected || r.channel == nil || r.channel.IsClosed() {
		log.Printf("No se puede suscribir, la conexion no esta disponible o el canal esta cerrado")
		return nil, fmt.Errorf("no hay conexion disponible para suscribirse")
	}

	log.Printf("Intentando Consume() en cola '%s'", queue)

	deliveries, err := r.channel.Consume(
		queue,   // queue
		"",      // consumer
		autoAck, // auto-ack
		false,   // exclusive
		false,   // no-local
		false,   // no-wait
		nil,     // args
	)

	if err != nil {
		log.Printf("Error al llamar a Consume(): %v", err)
		return nil, err
	}

	log.Printf("Suscripcion exitosa a cola '%s'", queue)
	return deliveries, nil
}