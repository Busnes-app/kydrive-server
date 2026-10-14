package editor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignedCallbacksAndRestrictedOutput(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "not authorized", 403) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cache/redirect" {
			http.Redirect(w, r, destination.URL, 302)
			return
		}
		w.Write([]byte("document"))
	}))
	defer server.Close()
	t.Setenv("KYDRIVE_EDITOR_URL", server.URL)
	t.Setenv("KYDRIVE_EDITOR_SECRET", strings.Repeat("s", 32))
	c, err := FromEnv("http://drive.test", "development")
	if err != nil {
		t.Fatal(err)
	}
	token, err := c.Sign(map[string]any{"payload": Callback{Key: "file-1", Status: 2, URL: server.URL + "/cache/document"}, "exp": time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	cb, err := c.Callback(token)
	if err != nil || cb.Status != 2 {
		t.Fatal(err)
	}
	if _, err = c.Callback(token + "tampered"); err == nil {
		t.Fatal("forged signature accepted")
	}
	expired, _ := c.Sign(map[string]any{"key": "file-1", "exp": time.Now().Add(-time.Minute).Unix()})
	if _, err = c.Callback(expired); err == nil {
		t.Fatal("expired token accepted")
	}
	for _, u := range []string{destination.URL + "/cache/document", server.URL + "/admin", server.URL + "/cache/redirect"} {
		if body, e := c.Fetch(context.Background(), u); e == nil {
			body.Close()
			t.Fatalf("unsafe output URL accepted: %s", u)
		}
	}
	if _, err = FromEnv("http://drive.test", "production"); err == nil {
		t.Fatal("production HTTP origin accepted")
	}
}

func TestDownloadTokenBindsTheExactURL(t *testing.T) {
	t.Setenv("KYDRIVE_EDITOR_URL", "http://editor.test")
	t.Setenv("KYDRIVE_EDITOR_SECRET", strings.Repeat("s", 32))
	c, err := FromEnv("http://drive.test", "development")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://drive.test/api/editor/download/a"
	sign := func(claims map[string]any) string {
		token, err := c.Sign(claims)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	if err = c.Download(sign(map[string]any{"payload": map[string]string{"url": url}, "exp": time.Now().Add(time.Minute).Unix()}), url); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"other session":   sign(map[string]any{"payload": map[string]string{"url": url + "b"}}),
		"editor config":   sign(map[string]any{"document": map[string]string{"url": url}}),
		"expired":         sign(map[string]any{"payload": map[string]string{"url": url}, "exp": time.Now().Add(-time.Minute).Unix()}),
		"missing":         "",
		"wrong signature": sign(map[string]any{"payload": map[string]string{"url": url}}) + "x",
	} {
		if c.Download(token, url) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
