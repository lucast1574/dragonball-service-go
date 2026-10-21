package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	name := flag.String("name", "", "Nombre del personaje")
	baseURL := flag.String("url", "http://localhost:8080", "Direccion del servicio")
	flag.Parse()
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "Uso: go run ./cmd/client -name Goku")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if err := createCharacter(*baseURL, strings.TrimSpace(*name)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func createCharacter(baseURL, name string) error {
	payload, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Post(strings.TrimRight(baseURL, "/")+"/characters", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("no se pudo consultar el servicio: %w", err)
	}
	defer response.Body.Close()
	const maxBody = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("leer respuesta: %w", err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("la respuesta del servicio es demasiado grande")
	}
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, bytes.TrimSpace(body))
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, body, "", "  "); err != nil {
		return fmt.Errorf("el servicio devolvio una respuesta JSON invalida: %w", err)
	}
	fmt.Println("Personaje guardado:")
	fmt.Println(formatted.String())
	return nil
}
