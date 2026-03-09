/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// IronicAPIVersion is the Ironic API microversion we use.
const IronicAPIVersion = "1.69"

// Client talks to OpenStack Ironic for node inventory and management operations.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	authToken  string
}

// NewClient creates an inventory client. baseURL is the Ironic API root.
func NewClient(httpClient *http.Client, baseURL *url.URL, authToken string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		authToken:  authToken,
	}
}

// Node holds Ironic node fields we care about.
type Node struct {
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	PowerState     string `json:"power_state"`
	ProvisionState string `json:"provision_state"`
}

// powerStateRequest is the body for PATCH .../states/power.
type powerStateRequest struct {
	Target string `json:"target"` // "on", "off", "reboot", etc.
}

// CheckConnectivity verifies we can reach the Ironic API (GET /v1/).
func (c *Client) CheckConnectivity(ctx context.Context) error {
	log := logf.FromContext(ctx).V(1)
	u := *c.baseURL
	u.Path = "/v1/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request ironic API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ironic API returned %d: %s", resp.StatusCode, string(body))
	}
	log.Info("Ironic connectivity check succeeded")
	return nil
}

// GetNode returns the node by UUID or name.
func (c *Client) GetNode(ctx context.Context, nodeID string) (*Node, error) {
	log := logf.FromContext(ctx).V(1)
	u := *c.baseURL
	u.Path = "/v1/nodes/" + url.PathEscape(nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", nodeID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get node %s: ironic returned %d: %s", nodeID, resp.StatusCode, string(body))
	}
	var node Node
	if err := json.NewDecoder(resp.Body).Decode(&node); err != nil {
		return nil, fmt.Errorf("decode node %s: %w", nodeID, err)
	}
	log.Info("Got node", "nodeID", nodeID, "power_state", node.PowerState, "provision_state", node.ProvisionState)
	return &node, nil
}

// SetPowerState sets the node power to "on" or "off" via Ironic API.
// target must be "on" or "off" (Ironic also supports "reboot", "soft off", etc.).
func (c *Client) SetPowerState(ctx context.Context, nodeID, target string) error {
	log := logf.FromContext(ctx)
	u := *c.baseURL
	u.Path = "/v1/nodes/" + url.PathEscape(nodeID) + "/states/power"
	body := powerStateRequest{Target: target}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal power state request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("set power state %s on node %s: %w", target, nodeID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set power state %s on node %s: ironic returned %d: %s", target, nodeID, resp.StatusCode, string(respBody))
	}
	log.Info("Set node power state", "nodeID", nodeID, "target", target)
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("X-Auth-Token", c.authToken)
	req.Header.Set("X-OpenStack-Ironic-API-Version", IronicAPIVersion)
}
