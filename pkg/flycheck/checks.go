package flycheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	suite "github.com/connyay/nats-cluster/pkg/check"
)

const Port = 5500

func StartCheckListener() {
	http.HandleFunc("/flycheck/vm", runVMChecks)

	slog.Info("Starting health check listener", "port", Port)
	http.ListenAndServe(fmt.Sprintf(":%d", Port), nil)
}

func runVMChecks(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	s := &suite.CheckSuite{Name: "VM"}
	s = CheckVM(s)
	s.Process(ctx)
	handleCheckResponse(w, s, false)
}

func handleCheckResponse(w http.ResponseWriter, suite *suite.CheckSuite, raw bool) {
	if suite.ErrOnSetup != nil {
		handleError(w, suite.ErrOnSetup)
		return
	}
	var result string
	if raw {
		result = suite.RawResult()
	} else {
		result = suite.Result()
	}
	if !suite.Passed() {
		handleError(w, errors.New(result))
		return
	}
	json.NewEncoder(w).Encode(result)
}

func handleError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(err.Error())
}
