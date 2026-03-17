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

package ironic

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/noauth"
	"github.com/gophercloud/gophercloud/v2/openstack/baremetal/v1/nodes"
)

// microversion is the Ironic API version required for common deploy and virtual-media flows.
const microversion = "1.69"

// Client talks to Ironic over REST via gophercloud.
type Client struct {
	serviceClient *gophercloud.ServiceClient
}

// ClientOptions configures the Ironic client.
type ClientOptions struct {
	InsecureSkipVerify bool
	HTTPClient         *http.Client
}

type tokenRoundTripper struct {
	rt    http.RoundTripper
	token string
}

func (t *tokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Auth-Token", t.token)
	return t.rt.RoundTrip(req)
}

// ensureV1Suffix returns base URL with trailing /v1 for gophercloud noauth.
func ensureV1Suffix(endpoint string) string {
	endpoint = strings.TrimSuffix(endpoint, "/")
	if strings.HasSuffix(endpoint, "/v1") {
		return endpoint
	}
	return endpoint + "/v1"
}

// NewClient creates an Ironic client with no-auth (typical for in-cluster Ironic with no Keystone).
func NewClient(ironicEndpoint string, opts ClientOptions) (*Client, error) {
	return newClient(ensureV1Suffix(ironicEndpoint), "", opts)
}

// NewClientWithToken creates an Ironic client that sends X-Auth-Token on every request
// (same pattern as Keystone / OSC tokens).
func NewClientWithToken(ironicEndpoint, authToken string, opts ClientOptions) (*Client, error) {
	return newClient(ensureV1Suffix(ironicEndpoint), authToken, opts)
}

func newClient(ironicEndpoint, authToken string, opts ClientOptions) (*Client, error) {
	client, err := noauth.NewBareMetalNoAuth(noauth.EndpointOpts{
		IronicEndpoint: ironicEndpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("create ironic client: %w", err)
	}

	client.Microversion = microversion

	baseTransport := http.DefaultTransport
	if opts.InsecureSkipVerify {
		if t, ok := baseTransport.(*http.Transport); ok {
			clone := t.Clone()
			clone.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			baseTransport = clone
		} else {
			baseTransport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		}
	}

	transport := baseTransport
	if authToken != "" {
		transport = &tokenRoundTripper{rt: baseTransport, token: authToken}
	}

	if opts.HTTPClient != nil {
		client.HTTPClient = *opts.HTTPClient
		if authToken != "" && opts.HTTPClient.Transport != nil {
			client.HTTPClient.Transport = &tokenRoundTripper{rt: opts.HTTPClient.Transport, token: authToken}
		}
	} else {
		client.HTTPClient = http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		}
	}

	return &Client{serviceClient: client}, nil
}

// GetNode fetches a node by UUID or name from Ironic
func (c *Client) GetNode(ctx context.Context, nodeID string) (*nodes.Node, error) {
	node, err := nodes.Get(ctx, c.serviceClient, nodeID).Extract()
	if err != nil {
		return nil, fmt.Errorf("get node %s: %w", nodeID, err)
	}
	return node, nil
}

// SetPowerState requests power on or off for the node via Ironic PUT .../states/power.
// target accepts "on"/"off" or Ironic values "power on"/"power off".
func (c *Client) SetPowerState(ctx context.Context, nodeID, target string) error {
	t := normalizePowerTarget(target)
	if t == "" {
		return fmt.Errorf("invalid power target %q: use on, off, power on, or power off", target)
	}
	res := nodes.ChangePowerState(ctx, c.serviceClient, nodeID, nodes.PowerStateOpts{Target: t})
	if err := res.ExtractErr(); err != nil {
		return fmt.Errorf("set power state on node %s: %w", nodeID, err)
	}
	return nil
}

func normalizePowerTarget(s string) nodes.TargetPowerState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "power on":
		return nodes.PowerOn
	case "off", "power off":
		return nodes.PowerOff
	default:
		return ""
	}
}

// UpdateNode applies a JSON Patch (RFC 6902) to the node.
func (c *Client) UpdateNode(ctx context.Context, nodeID string, patch nodes.UpdateOpts) (*nodes.Node, error) {
	updated, err := nodes.Update(ctx, c.serviceClient, nodeID, patch).Extract()
	if err != nil {
		return nil, fmt.Errorf("update node %s: %w", nodeID, err)
	}
	return updated, nil
}

// ValidateNode runs Ironic node validation.
func (c *Client) ValidateNode(ctx context.Context, nodeID string) (*nodes.NodeValidation, error) {
	v, err := nodes.Validate(ctx, c.serviceClient, nodeID).Extract()
	if err != nil {
		return nil, fmt.Errorf("validate node %s: %w", nodeID, err)
	}
	return v, nil
}

// ChangeProvisionState sets the node target provision state (e.g. active to deploy).
func (c *Client) ChangeProvisionState(ctx context.Context, nodeID string, opts nodes.ProvisionStateOpts) error {
	res := nodes.ChangeProvisionState(ctx, c.serviceClient, nodeID, opts)
	if res.Err != nil {
		return fmt.Errorf("change provision state for node %s: %w", nodeID, res.Err)
	}
	return nil
}
