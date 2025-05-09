package util

import (
	"encoding/json"
	"log/slog"
	"os"
)

type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func WriteError(err error) {
	resp := &Response{
		Success: false,
		Message: err.Error(),
	}
	sendToStdout(resp)
}

func WriteOutput(message, data string) {
	resp := &Response{
		Success: true,
		Message: message,
		Data:    data,
	}
	sendToStdout(resp)
}

func sendToStdout(resp *Response) {
	e, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to marshal response", "error", err)
	}
	slog.Info(string(e))
	os.Exit(0)
}
