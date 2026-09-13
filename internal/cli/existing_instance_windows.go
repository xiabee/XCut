//go:build windows

package cli

import (
	"net/http"
	"time"
)

// openExistingInstance probes a running xcut serve/client on url and, if
// alive, opens it in the browser for the user. Reports whether the probe
// answered — a silent false leaves the caller's lock-conflict message.
func openExistingInstance(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/api/v1/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	openBrowser(baseURL)
	return true
}
