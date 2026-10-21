package characters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const gokuJSON = `{"id":1,"name":"Goku","race":"Saiyan","gender":"Male","ki":"60.000.000","affiliation":"Z Fighter"}`

func TestNewHandlerWithConfiguredAPI(t *testing.T) {
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/characters" || r.URL.Query().Get("name") != "Goku" {
			t.Errorf("unexpected API request: %s", r.URL)
		}
		_, _ = io.WriteString(w, "["+gokuJSON+"]")
	}))
	defer external.Close()
	saves := 0
	handler, err := NewHandler(external.URL+"/api///", func(_ context.Context, c character) error {
		saves++
		if c.ID != 1 || c.Name != "Goku" {
			t.Errorf("unexpected character: %+v", c)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/characters", strings.NewReader(`{"name":"Goku"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || saves != 1 {
		t.Fatalf("status=%d saves=%d body=%s", w.Code, saves, w.Body)
	}
}

func TestNewHandlerRejectsInvalidAPIURL(t *testing.T) {
	for _, baseURL := range []string{"", "localhost:8080", "/api", "ftp://example.com/api", "https:///api", "http://%invalid", "https://user:secret@example.com/api", "https://example.com/api?name=Goku", "https://example.com/api#fragment"} {
		t.Run(baseURL, func(t *testing.T) {
			handler, err := NewHandler(baseURL, func(context.Context, character) error { return nil })
			if err == nil || handler != nil {
				t.Fatal("expected invalid configuration to be rejected")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("configuration error leaked credentials")
			}
		})
	}
}

func TestCreateCharacter(t *testing.T) {
	calls, saves := 0, 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("name") != "Goku" {
			t.Error("name was not normalized")
		}
		_, _ = io.WriteString(w, "["+gokuJSON+"]")
	}))
	defer external.Close()
	a := api{client: external.Client(), baseURL: external.URL, save: func(ctx context.Context, c character) error {
		saves++
		if c.ID != 1 || c.Name != "Goku" || c.Race != "Saiyan" {
			t.Errorf("wrong character: %+v", c)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing deadline")
		}
		return nil
	}}
	r := httptest.NewRequest("POST", "/characters", strings.NewReader(`{"name":" Goku "}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.createCharacter(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("got %d: %s; want 201", w.Code, w.Body)
	}
	if calls != 1 || saves != 1 {
		t.Fatalf("calls=%d saves=%d", calls, saves)
	}
}

func TestCreateCharacterStorageErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"duplicate", errDuplicate, 409},
		{"database failure", errors.New("private driver details"), 503},
		{"database timeout", context.DeadlineExceeded, 504},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, saves := 0, 0
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = io.WriteString(w, "["+gokuJSON+"]")
			}))
			defer external.Close()
			a := api{client: external.Client(), baseURL: external.URL, save: func(context.Context, character) error {
				saves++
				return tc.err
			}}
			r := httptest.NewRequest("POST", "/characters", strings.NewReader(`{"name":"Goku"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.createCharacter(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s; want %d", w.Code, w.Body, tc.status)
			}
			if strings.Contains(w.Body.String(), "private") {
				t.Fatal("internal details leaked")
			}
			if calls != 1 || saves != 1 {
				t.Fatalf("calls=%d saves=%d", calls, saves)
			}
		})
	}
}

func TestCreateCharacterRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name, body, contentType string
		status                  int
	}{
		{"empty name", `{"name":" "}`, "application/json", 400},
		{"missing name", `{}`, "application/json", 400},
		{"null name", `{"name":null}`, "application/json", 400},
		{"duplicate name", `{"name":"Vegeta","name":"Goku"}`, "application/json", 400},
		{"duplicate null name", `{"name":"Goku","name":null}`, "application/json", 400},
		{"escaped duplicate name", `{"name":"Goku","na\u006de":"Goku"}`, "application/json", 400},
		{"wrong key case", `{"NAME":"Goku"}`, "application/json", 400},
		{"wrong type", `{"name":42}`, "application/json", 400},
		{"invalid JSON", `{"name":`, "application/json", 400},
		{"null object", `null`, "application/json", 400},
		{"extra field", `{"name":"Goku","admin":true}`, "application/json", 400},
		{"extra object", `{"name":"Goku"} {}`, "application/json", 400},
		{"long name", `{"name":"` + strings.Repeat("x", 201) + `"}`, "application/json", 400},
		{"control character", `{"name":"Go\nku"}`, "application/json", 400},
		{"large body", `{"name":"` + strings.Repeat("x", 5000) + `"}`, "application/json", 413},
		{"large trailing whitespace", `{"name":"Goku"}` + strings.Repeat(" ", 4096), "application/json", 413},
		{"wrong content type", `{"name":"Goku"}`, "text/plain", 415},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid input reached external API")
			}))
			defer external.Close()
			a := api{client: external.Client(), baseURL: external.URL, save: func(context.Context, character) error {
				t.Error("invalid input reached storage")
				return nil
			}}
			r := httptest.NewRequest("POST", "/characters", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			a.createCharacter(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s; want %d", w.Code, w.Body, tc.status)
			}
		})
	}
}

func TestExternalResponses(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       int
	}{
		{"valid", "[" + gokuJSON + "]", 200, 201},
		{"fallback", "[" + strings.Replace(gokuJSON, `"name":"Goku"`, `"character":"Goku"`, 1) + "]", 200, 201},
		{"empty name fallback", "[" + strings.Replace(gokuJSON, `"name":"Goku"`, `"name":" ","character":"Goku"`, 1) + "]", 200, 201},
		{"name priority", "[" + strings.Replace(gokuJSON, `"name":"Goku"`, `"name":"Goku","character":"Vegeta"`, 1) + "]", 200, 201},
		{"empty list", "[]", 200, 404},
		{"partial only", "[" + strings.Replace(gokuJSON, "Goku", "Goku Black", 1) + "]", 200, 404},
		{"ambiguous", "[" + gokuJSON + "," + gokuJSON + "]", 200, 502},
		{"missing name", "[" + strings.Replace(gokuJSON, `"name":"Goku",`, "", 1) + "]", 200, 502},
		{"invalid id", "[" + strings.Replace(gokuJSON, `"id":1`, `"id":0`, 1) + "]", 200, 502},
		{"missing field", "[" + strings.Replace(gokuJSON, `"race":"Saiyan"`, `"race":""`, 1) + "]", 200, 502},
		{"control in race", "[" + strings.Replace(gokuJSON, "Saiyan", `Sai\u0000yan`, 1) + "]", 200, 502},
		{"control in gender", "[" + strings.Replace(gokuJSON, "Male", `Ma\nle`, 1) + "]", 200, 502},
		{"control in ki", "[" + strings.Replace(gokuJSON, "60.000.000", `60\t000`, 1) + "]", 200, 502},
		{"control in affiliation", "[" + strings.Replace(gokuJSON, "Z Fighter", `Z\rFighter`, 1) + "]", 200, 502},
		{"wrong field type", "[" + strings.Replace(gokuJSON, `"id":1`, `"id":"1"`, 1) + "]", 200, 502},
		{"null", "null", 200, 502},
		{"object", gokuJSON, 200, 502},
		{"bad JSON", "<html>error</html>", 200, 502},
		{"large response", strings.Repeat(" ", (2<<20)+1), 200, 502},
		{"external failure", "{}", 500, 502},
		{"rate limit", "{}", 429, 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer external.Close()
			saved := false
			a := api{client: external.Client(), baseURL: external.URL, save: func(context.Context, character) error { saved = true; return nil }}
			r := httptest.NewRequest("POST", "/characters", strings.NewReader(`{"name":"Goku"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.createCharacter(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s; want %d", w.Code, w.Body, tc.want)
			}
			if saved != (tc.want == 201) {
				t.Fatal("unexpected persistence")
			}
		})
	}
}

func TestNetworkAndCancellation(t *testing.T) {
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer external.Close()
	client := external.Client()
	client.Timeout = 20 * time.Millisecond
	a := api{client: client, baseURL: external.URL, save: func(context.Context, character) error { t.Fatal("must not save"); return nil }}
	r := httptest.NewRequest("POST", "/characters", strings.NewReader(`{"name":"Goku"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.createCharacter(w, r)
	if w.Code != 504 {
		t.Fatalf("got %d", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.findCharacter(ctx, "Goku")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	external.Close()
	_, err = a.findCharacter(context.Background(), "Goku")
	if !errors.Is(err, errUpstream) {
		t.Fatalf("lost network error: %v", err)
	}
}

func TestQueryEncoding(t *testing.T) {
	name := "Goku & Vegeta?"
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != name || len(r.URL.Query()) != 1 {
			t.Error("query was not escaped")
		}
		fmt.Fprint(w, "[]")
	}))
	defer external.Close()
	a := api{client: external.Client(), baseURL: external.URL}
	_, err := a.findCharacter(context.Background(), name)
	if !errors.Is(err, errNotFound) {
		t.Fatal(err)
	}
}

func TestExpiredRequestReturnsTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	a := api{client: &http.Client{}, baseURL: "http://127.0.0.1:1", save: func(context.Context, character) error {
		t.Fatal("expired request must not save")
		return nil
	}}
	r := httptest.NewRequest("POST", "/characters", strings.NewReader(`{"name":"Goku"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.createCharacter(w, r)
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("got %d: %s; want JSON timeout", w.Code, w.Body)
	}
}
