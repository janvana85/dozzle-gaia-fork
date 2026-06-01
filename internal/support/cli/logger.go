package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func ConfigureLogger(level string) {
	alertOnly := strings.EqualFold(level, "alert")
	if alertOnly {
		level = "debug"
	}
	if level, err := zerolog.ParseLevel(level); err == nil {
		zerolog.SetGlobalLevel(level)
	} else {
		panic(err)
	}

	_, dev := os.LookupEnv("DEV")
	var writer io.Writer = os.Stderr

	if dev {
		writer = zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) {
			w.FieldsOrder = []string{"id", "from", "to", "since"}
		})
	}

	if alertOnly {
		writer = alertLogWriter{writer: writer}
	}

	log.Logger = zerolog.New(writer).With().Timestamp().Str("version", Version).Logger()
}

type alertLogWriter struct {
	writer io.Writer
}

func (w alertLogWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.SplitAfter(p, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !isAlertLogLine(line) {
			continue
		}
		if _, err := w.writer.Write(line); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func isAlertLogLine(line []byte) bool {
	var event struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err == nil && event.Message != "" {
		return isAlertLogMessage(event.Message)
	}
	return isAlertLogMessage(string(line))
}

func isAlertLogMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "alert") ||
		strings.Contains(message, "notification") ||
		strings.Contains(message, "ntfy")
}
