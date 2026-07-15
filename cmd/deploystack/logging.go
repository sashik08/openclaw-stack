package main

import (
	"io"
	"log"
	"os"
)

// setupLogging направляет стандартный logger одновременно в stdout и в файл,
// чтобы события (старты, ошибки, рестарты) были и в консоли, и на диске.
func setupLogging(logPath string) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.SetOutput(os.Stdout)
		log.Printf("не удалось открыть лог-файл %s: %v (пишу только в stdout)", logPath, err)
		return
	}
	log.SetOutput(io.MultiWriter(os.Stdout, f))
	log.SetFlags(log.LstdFlags)
}
