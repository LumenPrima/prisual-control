package vmix

import (
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// CameraInfo holds info about a camera discovered from vMix's input list.
type CameraInfo struct {
	InputNumber int
	IP          string
	Title       string
}

// vmixXML mirrors the relevant subset of vMix's /api XML response.
type vmixXML struct {
	XMLName   xml.Name      `xml:"vmix"`
	Inputs    vmixInputs    `xml:"inputs"`
	Overlays  vmixOverlays  `xml:"overlays"`
	Streaming string        `xml:"streaming"`
}

type vmixOverlays struct {
	Overlay []vmixOverlay `xml:"overlay"`
}

type vmixOverlay struct {
	Number int    `xml:"number,attr"`
	Input  string `xml:",chardata"`
}

type vmixInputs struct {
	Input []vmixInput `xml:"input"`
}

type vmixInput struct {
	Key      string `xml:"key,attr"`
	Number   int    `xml:"number,attr"`
	Type     string `xml:"type,attr"`
	Title    string `xml:"title,attr"`
	State    string `xml:"state,attr"`
	Position int    `xml:"position,attr"`
	// The text content often contains the source URL/path
	Value string `xml:",chardata"`
}

// DiscoverCameras queries vMix's HTTP API and returns cameras whose input type
// indicates a network source (Stream, BrowserSource, NDI, etc.) and whose
// source URL or title contains a routable IP address.
// DiscoverCameras returns cameras with extractable IPs and the total input count.
func DiscoverCameras(vmixHost string) (cameras []CameraInfo, totalInputs int, err error) {
	url := fmt.Sprintf("http://%s:8088/api", vmixHost)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, 0, fmt.Errorf("vmix discovery: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("vmix discovery read: %w", err)
	}

	var v vmixXML
	if err := xml.Unmarshal(body, &v); err != nil {
		return nil, 0, fmt.Errorf("vmix discovery parse: %w", err)
	}

	totalInputs = len(v.Inputs.Input)
	seen := map[string]bool{}

	for _, inp := range v.Inputs.Input {
		ip := extractIP(inp.Value)
		if ip == "" {
			ip = extractIP(inp.Title)
		}
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		cameras = append(cameras, CameraInfo{
			InputNumber: inp.Number,
			IP:          ip,
			Title:       inp.Title,
		})
	}

	return cameras, totalInputs, nil
}

// InputInfo holds basic info about any vMix input.
type InputInfo struct {
	Number int
	Title  string
	Type   string // vMix input type (e.g. "Colour", "Stream", "Audio", "NDI")
}

// DiscoverInputs queries vMix's HTTP API and returns all inputs.
func DiscoverInputs(vmixHost string) ([]InputInfo, error) {
	url := fmt.Sprintf("http://%s:8088/api", vmixHost)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("vmix inputs: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vmix inputs read: %w", err)
	}

	var v vmixXML
	if err := xml.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("vmix inputs parse: %w", err)
	}

	inputs := make([]InputInfo, 0, len(v.Inputs.Input))
	for _, inp := range v.Inputs.Input {
		inputs = append(inputs, InputInfo{
			Number: inp.Number,
			Title:  inp.Title,
			Type:   inp.Type,
		})
	}
	return inputs, nil
}

// OverlayState queries vMix and returns the input number on overlay 1 (0 if none).
func OverlayState(vmixHost string) (int, error) {
	url := fmt.Sprintf("http://%s:8088/api", vmixHost)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	var v vmixXML
	if err := xml.Unmarshal(body, &v); err != nil {
		return 0, err
	}
	for _, ov := range v.Overlays.Overlay {
		if ov.Number == 1 && ov.Input != "" {
			var n int
			fmt.Sscanf(ov.Input, "%d", &n)
			return n, nil
		}
	}
	return 0, nil
}

// IsStreaming queries vMix and returns true if streaming is active.
func IsStreaming(vmixHost string) (bool, error) {
	url := fmt.Sprintf("http://%s:8088/api", vmixHost)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	var v vmixXML
	if err := xml.Unmarshal(body, &v); err != nil {
		return false, err
	}
	return strings.EqualFold(v.Streaming, "True"), nil
}

// extractIP finds the first IPv4 address in a string.
func extractIP(s string) string {
	// Split on common delimiters and check each token
	for _, delim := range []string{"/", ":", "@", " ", "?", "&", "=", ",", "(", ")"} {
		s = strings.ReplaceAll(s, delim, " ")
	}
	for _, token := range strings.Fields(s) {
		ip := net.ParseIP(token)
		if ip != nil && ip.To4() != nil {
			return token
		}
	}
	return ""
}
