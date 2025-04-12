FROM golang:1.22-alpine AS builder

# Instalar dependencias de compilación
RUN apk add --no-cache git

# Establecer directorio de trabajo
WORKDIR /app

# Copiar archivos go.mod y go.sum primero para aprovechar la caché de capas de Docker
COPY go.mod go.sum ./
RUN go mod download

# Copiar el código fuente
COPY . .

# Compilar la aplicación
RUN go build -o processor ./cmd/processor

# Imagen final más ligera
FROM alpine:latest

# Instalar dependencias de runtime necesarias
RUN apk add --no-cache ca-certificates tzdata

# Crear un usuario no root para seguridad
RUN adduser -D -h /app appuser

# Crear directorios necesarios y configurar permisos
RUN mkdir -p /app/logs && \
    chown -R appuser:appuser /app

# Establecer directorio de trabajo
WORKDIR /app

# Copiar el binario compilado desde la etapa de compilación
COPY --from=builder /app/processor .

# Establecer variables de entorno
ENV RABBITMQ_SERVERS="amqp://jtorres:jtorres159.@161.132.50.183:5672" \
    QUEUE_IN="images-to-process" \
    QUEUE_OUT="processed-images" \
    QUEUE_DLX="images-process-failed" \
    QUEUE_DURABLE="true" \
    PREFETCH_COUNT="5" \
    MAX_WORKERS="6" \
    IMAGE_SIZE_THRESHOLD="1048576" \
    PROCESSING_TIMEOUT="30" \
    RABBITMQ_HEARTBEAT="120" \
    RABBITMQ_BLOCKED_CONNECTION_TIMEOUT="300" \
    RABBITMQ_CONNECTION_TIMEOUT="20000" \
    RABBITMQ_RETRY_DELAY="5" \
    DEBUG="true" \
    LOG_LEVEL="info" \
    LOG_TO_FILE="false" \
    LOG_DIR="/app/logs"

# Cambiar al usuario no root para seguridad
USER appuser

# Exponer puerto si es necesario (ajusta según tu aplicación)
# EXPOSE 8080

# Comando para ejecutar la aplicación
CMD ["./processor"]