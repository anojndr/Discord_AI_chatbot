// In a new file, e.g., internal/net/http.go
package net

import (
    "net/http"
    "time"
)

var defaultTransport = &http.Transport{
    MaxIdleConns:          100,
    MaxIdleConnsPerHost:   100,
    IdleConnTimeout:       90 * time.Second,
    TLSHandshakeTimeout:   10 * time.Second,
    ExpectContinueTimeout: 1 * time.Second,
}

func NewOptimizedClient(timeout time.Duration) *http.Client {
    return &http.Client{
        Timeout:   timeout,
        Transport: defaultTransport,
    }
}