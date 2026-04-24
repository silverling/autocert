package cloudflare

import (
	"fmt"
	"strings"
)

type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
	TTL     int    `json:"ttl,omitempty"`
}

type APIResponse[T any] struct {
	Success bool      `json:"success,omitempty"`
	Errors  Errors    `json:"errors,omitempty"`
	Result  T         `json:"result,omitempty"`
	Message []Message `json:"messages,omitempty"`
}

type Message struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Errors []Message

func (e Errors) Error() string {
	var parts []string
	for _, item := range e {
		parts = append(parts, fmt.Sprintf("%d: %s", item.Code, item.Message))
	}
	return strings.Join(parts, "; ")
}

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
