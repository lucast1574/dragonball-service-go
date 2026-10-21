package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

var (
	errNotFound  = errors.New("character not found")
	errDuplicate = errors.New("character already stored")
	errUpstream  = errors.New("DragonBall API unavailable or invalid response")
)

type character struct {
	ID          int64  `json:"id" bson:"_id"`
	Name        string `json:"name" bson:"name"`
	Race        string `json:"race" bson:"race"`
	Gender      string `json:"gender" bson:"gender"`
	Ki          string `json:"ki" bson:"ki"`
	Affiliation string `json:"affiliation" bson:"affiliation"`
}

type externalCharacter struct {
	character
	AlternateName string `json:"character"`
}

type api struct {
	client  *http.Client
	baseURL string
	save    func(context.Context, character) error
}

// NewHandler connects the endpoint to its external API and persistence function.
func NewHandler(baseURL string, save func(context.Context, character) error) (http.Handler, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid DRAGONBALL_API_URL: expected an HTTP(S) base URL without credentials, query or fragment")
	}
	a := &api{
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		save:    save,
	}
	return http.HandlerFunc(a.createCharacter), nil
}

func (a *api) createCharacter(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, 415, "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	name, err := readName(r.Body)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || strings.ContainsFunc(name, unicode.IsControl) {
		writeError(w, 400, "name must contain 1 to 200 bytes without control characters")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	c, err := a.findCharacter(ctx, name)
	if err != nil {
		respondError(w, r, err)
		return
	}
	if err := a.save(ctx, c); err != nil {
		respondError(w, r, err)
		return
	}
	slog.InfoContext(r.Context(), "character created", "id", c.ID, "name", c.Name)
	writeJSON(w, http.StatusCreated, c)
}

// readName accepts exactly one name field, avoiding ambiguous duplicate keys.
func readName(body io.Reader) (string, error) {
	d := json.NewDecoder(body)
	for _, expected := range []any{json.Delim('{'), "name"} {
		token, err := d.Token()
		if err != nil {
			return "", err
		}
		if token != expected {
			return "", errors.New("expected one name field")
		}
	}
	var name string
	if err := d.Decode(&name); err != nil {
		return "", err
	}
	token, err := d.Token()
	if err != nil {
		return "", err
	}
	if token != json.Delim('}') {
		return "", errors.New("unexpected field after name")
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		if err != nil {
			return "", err
		}
		return "", errors.New("expected end of JSON body")
	}
	return name, nil
}

func (a *api) findCharacter(ctx context.Context, name string) (character, error) {
	endpoint := a.baseURL + "/characters?" + url.Values{"name": {name}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return character{}, fmt.Errorf("%w: %w", errUpstream, err)
	}
	req.Header.Set("Accept", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return character{}, fmt.Errorf("%w: %w", errUpstream, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return character{}, fmt.Errorf("%w: HTTP %d", errUpstream, res.StatusCode)
	}
	const maxResponse = 2 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return character{}, fmt.Errorf("%w: %w", errUpstream, err)
	}
	if len(body) > maxResponse {
		return character{}, fmt.Errorf("%w: response too large", errUpstream)
	}
	var items []externalCharacter
	if err := json.Unmarshal(body, &items); err != nil {
		return character{}, fmt.Errorf("%w: %w", errUpstream, err)
	}
	if items == nil {
		return character{}, fmt.Errorf("%w: expected array", errUpstream)
	}
	return selectCharacter(items, name)
}

func selectCharacter(items []externalCharacter, name string) (character, error) {
	var match character
	var found bool
	for _, item := range items {
		c := item.character
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			c.Name = strings.TrimSpace(item.AlternateName)
		}
		if c.Name == "" {
			return character{}, fmt.Errorf("%w: missing name", errUpstream)
		}
		if !strings.EqualFold(c.Name, name) {
			continue
		}
		if found || !validCharacter(c) {
			return character{}, fmt.Errorf("%w: ambiguous or incomplete character", errUpstream)
		}
		match = c
		found = true
	}
	if !found {
		return character{}, errNotFound
	}
	return match, nil
}

func validCharacter(c character) bool {
	if c.ID <= 0 {
		return false
	}
	for _, field := range []string{c.Name, c.Race, c.Gender, c.Ki, c.Affiliation} {
		if strings.TrimSpace(field) == "" || strings.ContainsFunc(field, unicode.IsControl) {
			return false
		}
	}
	return true
}

func respondError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(r.Context().Err(), context.Canceled) {
		return
	} // Caller is no longer waiting.
	if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		err = context.DeadlineExceeded
	}
	status, message := 503, "Character storage is unavailable"
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		status, message = 504, "A dependency exceeded its time limit"
	case errors.Is(err, errNotFound):
		status, message = 404, "Character not found"
	case errors.Is(err, errDuplicate):
		status, message = 409, "Character is already stored"
	case errors.Is(err, errUpstream):
		status, message = 502, "DragonBall API is unavailable or returned invalid data"
	}
	if status >= 500 {
		slog.ErrorContext(r.Context(), "create character failed", "status", status, "error", err)
	}
	writeError(w, status, message)
}

func writeBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, 413, "Request body exceeds 4096 bytes")
		return
	}
	writeError(w, 400, "Expected one JSON object with a string name")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value) // A disconnected client cannot receive a replacement response.
}
