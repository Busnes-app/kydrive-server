package editor

import (
	"context"
	"encoding/json"
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

func TestConvertSignsTheRequestAndReturnsTheOutput(t *testing.T) {
	var got map[string]any
	var answer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/converter" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(answer))
	}))
	defer server.Close()
	t.Setenv("KYDRIVE_EDITOR_URL", server.URL)
	t.Setenv("KYDRIVE_EDITOR_SECRET", strings.Repeat("s", 32))
	c, err := FromEnv("http://drive.test", "development")
	if err != nil {
		t.Fatal(err)
	}
	answer = `{"endConvert":true,"fileType":"png","fileUrl":"` + server.URL + `/cache/files/data/thumb/output.png","percent":100}`
	out, err := c.Convert(context.Background(), "thumb-1", "docx", "http://drive.test/api/editor/convert/t1", 256)
	if err != nil || out != server.URL+"/cache/files/data/thumb/output.png" {
		t.Fatalf("convert %q %v", out, err)
	}
	if got["filetype"] != "docx" || got["outputtype"] != "png" || got["url"] != "http://drive.test/api/editor/convert/t1" || got["key"] != "thumb-1" || got["async"] != false {
		t.Fatalf("request %v", got)
	}
	claims, err := c.Verify(got["token"].(string))
	if err != nil || !strings.Contains(string(claims["url"]), "/api/editor/convert/t1") || !strings.Contains(string(claims["thumbnail"]), `"first":true`) {
		t.Fatalf("token %v %v", claims, err)
	}
	for name, body := range map[string]string{
		"error":      `{"error":-4}`,
		"unfinished": `{"endConvert":false,"percent":40}`,
		"no url":     `{"endConvert":true}`,
		"not json":   `<html>`,
		"other host": `{"endConvert":true,"fileUrl":"http://elsewhere.test/cache/x.png"}`,
	} {
		answer = body
		if _, err := c.Convert(context.Background(), "thumb-1", "docx", "http://drive.test/x", 256); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// ConvertText asks for plain text, signed like a thumbnail conversion, and checks its output URL
// the same way.
func TestConvertTextAsksForTxt(t *testing.T) {
	var got map[string]any
	var answer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(answer))
	}))
	defer server.Close()
	t.Setenv("KYDRIVE_EDITOR_URL", server.URL)
	t.Setenv("KYDRIVE_EDITOR_SECRET", strings.Repeat("s", 32))
	c, err := FromEnv("http://drive.test", "development")
	if err != nil {
		t.Fatal(err)
	}
	answer = `{"endConvert":true,"fileType":"txt","fileUrl":"` + server.URL + `/cache/files/data/text/output.txt","percent":100}`
	out, err := c.ConvertText(context.Background(), "text-1", "pdf", "http://drive.test/api/editor/convert/t1")
	if err != nil || out != server.URL+"/cache/files/data/text/output.txt" {
		t.Fatalf("convert %q %v", out, err)
	}
	if got["filetype"] != "pdf" || got["outputtype"] != "txt" || got["thumbnail"] != nil || got["key"] != "text-1" {
		t.Fatalf("request %v", got)
	}
	if claims, err := c.Verify(got["token"].(string)); err != nil || !strings.Contains(string(claims["outputtype"]), "txt") {
		t.Fatalf("token %v %v", claims, err)
	}
	answer = `{"endConvert":true,"fileUrl":"http://elsewhere.test/cache/x.txt"}`
	if _, err := c.ConvertText(context.Background(), "text-1", "pdf", "http://drive.test/x"); err == nil {
		t.Error("output on another host accepted")
	}
}
