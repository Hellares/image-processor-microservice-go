package logger

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Log es el logger global
	Log *logrus.Logger
)

// InitLogger inicializa el logger
func InitLogger(logLevel string, logToFile bool, logDir string) {
	Log = logrus.New()

	// Configurar formato
	Log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "level",
			logrus.FieldKeyMsg:   "message",
		},
	})

	// Configurar nivel de log
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	Log.SetLevel(level)

	// Configurar salidas
	outputs := []io.Writer{os.Stdout} // Siempre enviamos logs a stdout

	// Si se solicita log a archivo
	if logToFile {
		// Crear directorio si no existe
		if _, err := os.Stat(logDir); os.IsNotExist(err) {
			if err := os.MkdirAll(logDir, 0755); err != nil {
				Log.Warnf("No se pudo crear el directorio de logs: %v", err)
			}
		}

		// Nombre de archivo con fecha
		today := time.Now().Format("2006-01-02")
		logPath := filepath.Join(logDir, "processor-"+today+".log")

		// Configurar rotación de logs con lumberjack
		logFile := &lumberjack.Logger{
			Filename:   logPath,
			MaxSize:    100, // MB
			MaxBackups: 10,  // Número de archivos de backup
			MaxAge:     30,  // Días
			Compress:   true,
		}

		outputs = append(outputs, logFile)
	}

	// Combinar salidas
	if len(outputs) > 1 {
		Log.SetOutput(io.MultiWriter(outputs...))
	}

	Log.Info("Sistema de logs inicializado")
}

// GetLogger devuelve un logger con campos para un componente específico
func GetLogger(component string) *logrus.Entry {
	return Log.WithField("component", component)
}