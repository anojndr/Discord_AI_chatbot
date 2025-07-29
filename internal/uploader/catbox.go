package uploader

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sync"
	"time"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

const catboxAPIURL = "https://catbox.moe/user/api.php"

// UploadToCatbox uploads a file to Catbox.moe and returns the URL.
func UploadToCatbox(filename string, fileData []byte) (string, error) {
	body := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		body.Reset()
		bufferPool.Put(body)
	}()
	writer := multipart.NewWriter(body)

	// Add the request type
	if err := writer.WriteField("reqtype", "fileupload"); err != nil {
		return "", fmt.Errorf("failed to write reqtype field: %w", err)
	}

	// Add the file
	part, err := writer.CreateFormFile("fileToUpload", filename)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(fileData)); err != nil {
		return "", fmt.Errorf("failed to copy file data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequest("POST", catboxAPIURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{
		Timeout: 10 * time.Minute, // 10 minute timeout for the entire request
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = 30 * time.Second // 30 seconds for TLS handshake
	client.Transport = transport

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// We can't do much here, but we should log it.
			_ = err
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code: %s", resp.Status)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(respBody), nil
}
