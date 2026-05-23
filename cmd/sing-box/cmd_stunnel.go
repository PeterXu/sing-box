package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/spf13/cobra"
)

var commandStunnelRemoveForce bool
var commandStunnelApplyForce bool
var commandStunnelApplyMode string

var commandStunnel = &cobra.Command{
	Use:   "stunnel",
	Short: "Manage stunnel outbound groups",
}

var commandStunnelList = &cobra.Command{
	Use:   "list [group]",
	Short: "List stunnel groups or show group details",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		if len(args) == 0 {
			err = stunnelListGroups(addr, secret, client)
		} else {
			err = stunnelListGroup(addr, secret, client, args[0])
		}
		if err != nil {
			log.Fatal(err)
		}
	},
}

var commandStunnelRemove = &cobra.Command{
	Use:   "remove <group> <outbound...>",
	Short: "Remove outbounds from a stunnel group",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		err = stunnelRemove(addr, secret, client, args[0], args[1:], commandStunnelRemoveForce)
		if err != nil {
			log.Fatal(err)
		}
	},
}

var commandStunnelURL = &cobra.Command{
	Use:   "url <group> <url>",
	Short: "Update health check URL of a stunnel group",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		err = stunnelSetURL(addr, secret, client, args[0], args[1])
		if err != nil {
			log.Fatal(err)
		}
	},
}

var commandStunnelApply = &cobra.Command{
	Use:   "apply <config-file>",
	Short: "Apply stunnel group config from file (create/update outbounds and URL)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		err = stunnelApply(addr, secret, client, args[0], commandStunnelApplyForce, commandStunnelApplyMode)
		if err != nil {
			log.Fatal(err)
		}
	},
}

var commandStunnelExport = &cobra.Command{
	Use:   "export [output-file]",
	Short: "Export all stunnel groups config (outbounds and URL)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		outputFile := ""
		if len(args) > 0 {
			outputFile = args[0]
		}
		err = stunnelExport(addr, secret, client, outputFile)
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandStunnelRemove.Flags().BoolVar(&commandStunnelRemoveForce, "force", false, "proceed even if removing active outbound")
	commandStunnelApply.Flags().BoolVar(&commandStunnelApplyForce, "force", false, "proceed even if removing active outbound")
	commandStunnelApply.Flags().StringVar(&commandStunnelApplyMode, "mode", "replace", "apply mode: replace (default), add, remove")
	commandStunnel.AddCommand(commandStunnelList, commandStunnelRemove, commandStunnelURL, commandStunnelApply, commandStunnelExport)
	mainCommand.AddCommand(commandStunnel)
}

func clashAPIConfig() (addr string, secret string, err error) {
	options, err := readConfigAndMerge()
	if err != nil {
		return "", "", E.Cause(err, "read config")
	}
	if options.Experimental == nil || options.Experimental.ClashAPI == nil || options.Experimental.ClashAPI.ExternalController == "" {
		return "", "", E.New("clash_api not configured. Add experimental.clash_api.external_controller to your config.")
	}
	addr = options.Experimental.ClashAPI.ExternalController
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	secret = options.Experimental.ClashAPI.Secret
	return
}

func clashAPIRequest(client *http.Client, method, url, secret string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, E.Cause(err, "request failed")
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, E.New("not found: ", url)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, E.New("unauthorized: check clash_api secret")
	}
	if resp.StatusCode >= 400 {
		return nil, E.New("HTTP ", fmt.Sprint(resp.StatusCode), ": ", string(respBody))
	}
	return respBody, nil
}

type proxyInfo struct {
	Name    string              `json:"name"`
	Type    string              `json:"type"`
	Now     string              `json:"now"`
	All     []string            `json:"all"`
	URL     string              `json:"url,omitempty"`
	History []proxyHistoryEntry `json:"history"`
}

type proxyHistoryEntry struct {
	Time  string `json:"time"`
	Delay uint16 `json:"delay"`
}

type proxiesResponse struct {
	Proxies map[string]json.RawMessage `json:"proxies"`
}

func getGroupInfo(baseURL, secret string, client *http.Client, group string) (*proxyInfo, error) {
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies/"+url.PathEscape(group), secret, nil)
	if err != nil {
		return nil, err
	}
	var info proxyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, E.Cause(err, "parse response")
	}
	return &info, nil
}

func setGroupMembers(baseURL, secret string, client *http.Client, group string, tags []string) error {
	body := map[string][]string{"outbounds": tags}
	_, err := clashAPIRequest(client, http.MethodPut, baseURL+"/proxies/"+url.PathEscape(group)+"/members", secret, body)
	return err
}

func getMemberDelay(baseURL, secret string, client *http.Client, tag string) uint16 {
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies/"+url.PathEscape(tag), secret, nil)
	if err != nil {
		return 0
	}
	var info proxyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return 0
	}
	if len(info.History) > 0 {
		return info.History[0].Delay
	}
	return 0
}

func stunnelListGroups(baseURL, secret string, client *http.Client) error {
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies", secret, nil)
	if err != nil {
		return err
	}
	var resp proxiesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return E.Cause(err, "parse response")
	}
	var groups []string
	for name, raw := range resp.Proxies {
		var info proxyInfo
		if json.Unmarshal(raw, &info) == nil && info.Type == "Stunnel" {
			groups = append(groups, name)
		}
	}
	if len(groups) == 0 {
		fmt.Println("No stunnel groups found.")
		return nil
	}
	for _, g := range groups {
		fmt.Println(g)
	}
	return nil
}

func stunnelListGroup(baseURL, secret string, client *http.Client, group string) error {
	info, err := getGroupInfo(baseURL, secret, client, group)
	if err != nil {
		return err
	}
	if info.Type != "Stunnel" {
		return E.New(group, " is not a stunnel group (type: ", info.Type, ")")
	}
	fmt.Println("Group:", info.Name)
	if info.Now != "" {
		delay := getMemberDelay(baseURL, secret, client, info.Now)
		if delay > 0 {
			fmt.Printf("Now: %s (%dms)\n", info.Now, delay)
		} else {
			fmt.Println("Now:", info.Now)
		}
	}
	if info.URL != "" {
		fmt.Println("URL:", info.URL)
	}
	fmt.Println("All:")
	for _, tag := range info.All {
		suffix := ""
		if tag == info.Now {
			suffix = " (active)"
		}
		delay := getMemberDelay(baseURL, secret, client, tag)
		if delay > 0 {
			fmt.Printf("  %s\t%dms%s\n", tag, delay, suffix)
		} else {
			fmt.Printf("  %s\t%s\n", tag, suffix)
		}
	}
	return nil
}

func stunnelRemove(baseURL, secret string, client *http.Client, group string, tags []string, force bool) error {
	info, err := getGroupInfo(baseURL, secret, client, group)
	if err != nil {
		return err
	}
	removeSet := make(map[string]bool)
	for _, t := range tags {
		// Check if it's an internal type - cannot be removed
		obType := getOutboundType(baseURL, secret, client, t)
		if isInternalType(obType) {
			fmt.Fprintf(os.Stderr, "Warning: skipping internal outbound %q (type: %s)\n", t, obType)
			continue
		}
		removeSet[t] = true
	}
	if len(removeSet) == 0 {
		return E.New("no valid outbounds to remove (all are internal types or not found)")
	}
	if info.Now != "" && removeSet[info.Now] && !force {
		return E.New(fmt.Sprintf("outbound %q is currently active. Use --force to proceed.", info.Now))
	}
	var newList []string
	for _, t := range info.All {
		if removeSet[t] {
			if info.Now == t && force {
				fmt.Fprintf(os.Stderr, "Warning: removing active outbound %q, server will re-select.\n", t)
			}
			delete(removeSet, t)
			continue
		}
		newList = append(newList, t)
	}
	for t := range removeSet {
		fmt.Println("skip not-existing:", t)
	}
	if err := setGroupMembers(baseURL, secret, client, group, newList); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}

func stunnelSetURL(baseURL, secret string, client *http.Client, group, checkURL string) error {
	body := map[string]string{"url": checkURL}
	_, err := clashAPIRequest(client, http.MethodPut, baseURL+"/proxies/"+url.PathEscape(group)+"/url", secret, body)
	if err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}

type stunnelGroupConfig struct {
	Outbounds []json.RawMessage `json:"outbounds"`
	URL       string            `json:"url,omitempty"`
}

// Protocol types that can be dynamically created/deleted (display names from Clash API)
var protocolOutboundTypes = map[string]bool{
	"VMess": true, "VLESS": true, "Trojan": true, "Shadowsocks": true,
	"ShadowTLS": true, "SOCKS": true, "HTTP": true, "WireGuard": true,
	"TUIC": true, "Hysteria2": true, "Hysteria": true, "Naive": true,
	"AnyTLS": true,
}

// Internal types that are pre-configured and should never be modified (display names from Clash API)
// Note: Block is displayed as "Reject" in Clash API
var internalOutboundTypes = map[string]bool{
	"Reject": true, "Direct": true, "Stunnel": true,
	"Selector": true, "URLTest": true, "DNS": true,
}

func isProtocolType(t string) bool {
	return protocolOutboundTypes[t]
}

func isInternalType(t string) bool {
	return internalOutboundTypes[t]
}

func getOutboundType(baseURL, secret string, client *http.Client, tag string) string {
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies/"+url.PathEscape(tag), secret, nil)
	if err != nil {
		return ""
	}
	var info proxyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return ""
	}
	return info.Type
}

func deleteOutbound(baseURL, secret string, client *http.Client, tag string) error {
	_, err := clashAPIRequest(client, http.MethodDelete, baseURL+"/proxies/"+url.PathEscape(tag), secret, nil)
	return err
}

func stunnelApply(baseURL, secret string, client *http.Client, configFile string, force bool, mode string) error {
	// Validate mode
	switch mode {
	case "replace", "add", "remove":
		break
	default:
		return E.New("invalid mode: ", mode, " (valid: replace, add, remove)")
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return E.Cause(err, "read config file")
	}
	var config map[string]stunnelGroupConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return E.Cause(err, "parse config file")
	}
	for group, cfg := range config {
		fmt.Printf("Applying config for group: %s (mode: %s)\n", group, mode)

		// Get current group info
		info, err := getGroupInfo(baseURL, secret, client, group)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Error getting group info: %v\n", err)
			continue
		}

		// Parse config outbounds (must be full objects, not tag strings)
		var configOutbounds []map[string]interface{}
		var configTags []string
		for _, outboundRaw := range cfg.Outbounds {
			var rawConfig map[string]interface{}
			if err := json.Unmarshal(outboundRaw, &rawConfig); err != nil {
				// Check if it's a string (tag reference) - not allowed
				var tagStr string
				if err := json.Unmarshal(outboundRaw, &tagStr); err == nil {
					fmt.Fprintf(os.Stderr, "  Error: tag string \"%s\" not allowed, must provide full outbound config\n", tagStr)
					continue
				}
				fmt.Fprintf(os.Stderr, "  Error parsing outbound config: %v\n", err)
				continue
			}

			// Validate type
			outboundType, ok := rawConfig["type"].(string)
			if !ok || outboundType == "" {
				fmt.Fprintf(os.Stderr, "  Error: outbound config missing 'type' field\n")
				continue
			}
			if !isProtocolType(outboundType) {
				fmt.Fprintf(os.Stderr, "  Error: outbound type \"%s\" is not allowed (internal types: block/direct/stunnel/selector/urltest/dns cannot be modified)\n", outboundType)
				continue
			}

			// Validate tag
			tag, ok := rawConfig["tag"].(string)
			if !ok || tag == "" {
				fmt.Fprintf(os.Stderr, "  Error: outbound config missing 'tag' field\n")
				continue
			}

			configOutbounds = append(configOutbounds, rawConfig)
			configTags = append(configTags, tag)
		}

		if len(configTags) == 0 && mode != "remove" {
			fmt.Fprintf(os.Stderr, "  Error: no valid outbounds in config\n")
			continue
		}

		// Separate existing outbounds into internal and protocol
		var internalTags []string
		var protocolTags []string
		for _, tag := range info.All {
			obType := getOutboundType(baseURL, secret, client, tag)
			if isInternalType(obType) {
				internalTags = append(internalTags, tag)
			} else if isProtocolType(obType) {
				protocolTags = append(protocolTags, tag)
			}
		}

		// Apply mode to determine final outbounds
		var finalTags []string
		var toCreate []map[string]interface{}
		var toDelete []string

		switch mode {
		case "replace":
			// Keep internal, delete protocol not in config, create new from config
			configTagSet := make(map[string]bool)
			for _, t := range configTags {
				configTagSet[t] = true
			}

			// Find protocol outbounds to delete (not in new config)
			for _, t := range protocolTags {
				if !configTagSet[t] {
					toDelete = append(toDelete, t)
					fmt.Printf("  Deleting outbound: %s\n", t)
				}
			}

			// Find outbounds to create (not existing or existing with different config)
			existingTagSet := make(map[string]bool)
			for _, t := range info.All {
				existingTagSet[t] = true
			}
			for _, rawConfig := range configOutbounds {
				tag := rawConfig["tag"].(string)
				if !existingTagSet[tag] {
					toCreate = append(toCreate, rawConfig)
				} else {
					// Already exists, keep it (config should match)
					fmt.Printf("  Keeping existing outbound: %s\n", tag)
				}
			}

			finalTags = append(finalTags, internalTags...)
			finalTags = append(finalTags, configTags...)

		case "add":
			// Keep all existing + add new (skip duplicate tags)
			existingTagSet := make(map[string]bool)
			for _, t := range info.All {
				existingTagSet[t] = true
			}

			finalTags = append(finalTags, info.All...)

			for _, rawConfig := range configOutbounds {
				tag := rawConfig["tag"].(string)
				if existingTagSet[tag] {
					fmt.Printf("  Outbound %s already exists, skipping\n", tag)
				} else {
					toCreate = append(toCreate, rawConfig)
					finalTags = append(finalTags, tag)
					fmt.Printf("  Adding outbound: %s\n", tag)
				}
			}

		case "remove":
			// Keep internal + remove matching protocol tags
			removeTagSet := make(map[string]bool)
			for _, t := range configTags {
				removeTagSet[t] = true
			}

			finalTags = append(finalTags, internalTags...)

			for _, t := range protocolTags {
				if removeTagSet[t] {
					toDelete = append(toDelete, t)
					fmt.Printf("  Removing outbound: %s\n", t)
				} else {
					finalTags = append(finalTags, t)
				}
			}

			if len(finalTags) == 0 {
				fmt.Fprintf(os.Stderr, "  Error: cannot remove all outbounds from group\n")
				continue
			}
		}

		// Delete old outbounds
		for _, tag := range toDelete {
			if err := deleteOutbound(baseURL, secret, client, tag); err != nil {
				fmt.Fprintf(os.Stderr, "  Error deleting outbound %s: %v\n", tag, err)
			}
		}

		// Create new outbounds
		for _, rawConfig := range toCreate {
			tag := rawConfig["tag"].(string)
			_, err := clashAPIRequest(client, http.MethodPost, baseURL+"/proxies", secret, rawConfig)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Error creating outbound %s: %v\n", tag, err)
				continue
			}
			fmt.Printf("  Created outbound: %s\n", tag)
		}

		// Check if current active is in final list
		if info.Now != "" {
			inFinal := false
			for _, t := range finalTags {
				if t == info.Now {
					inFinal = true
					break
				}
			}
			if !inFinal {
				if !force {
					fmt.Fprintf(os.Stderr, "  Error: active outbound %q not in final list. Use --force to proceed.\n", info.Now)
					continue
				}
				fmt.Fprintf(os.Stderr, "  Warning: active outbound %q not in final list, server will re-select.\n", info.Now)
			}
		}

		if len(finalTags) == 0 {
			fmt.Fprintf(os.Stderr, "  Warning: no valid outbounds to set, skipping update\n")
			continue
		}

		if err := setGroupMembers(baseURL, secret, client, group, finalTags); err != nil {
			fmt.Fprintf(os.Stderr, "  Error updating outbounds: %v\n", err)
			continue
		}
		fmt.Printf("  Updated outbounds: %v\n", finalTags)

		if cfg.URL != "" {
			if err := stunnelSetURLQuiet(baseURL, secret, client, group, cfg.URL); err != nil {
				fmt.Fprintf(os.Stderr, "  Error updating URL: %v\n", err)
				continue
			}
			fmt.Printf("  Updated URL: %s\n", cfg.URL)
		}
	}
	fmt.Println("Done.")
	return nil
}

func stunnelSetURLQuiet(baseURL, secret string, client *http.Client, group, checkURL string) error {
	body := map[string]string{"url": checkURL}
	_, err := clashAPIRequest(client, http.MethodPut, baseURL+"/proxies/"+url.PathEscape(group)+"/url", secret, body)
	return err
}

func stunnelExport(baseURL, secret string, client *http.Client, outputFile string) error {
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies", secret, nil)
	if err != nil {
		return err
	}
	var resp proxiesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return E.Cause(err, "parse response")
	}

	config := make(map[string]stunnelGroupConfig)
	for name, raw := range resp.Proxies {
		var info proxyInfo
		if json.Unmarshal(raw, &info) == nil && info.Type == "Stunnel" {
			// Export only protocol outbounds (not internal types like direct/block)
			outbounds := make([]json.RawMessage, 0)
			for _, tag := range info.All {
				obType := getOutboundType(baseURL, secret, client, tag)
				if isProtocolType(obType) {
					// Note: API doesn't provide full outbound config, only tag
					// Users need to maintain full configs separately
					tagJSON, _ := json.Marshal(tag)
					outbounds = append(outbounds, json.RawMessage(tagJSON))
				}
			}
			config[name] = stunnelGroupConfig{
				Outbounds: outbounds,
				URL:       info.URL,
			}
		}
	}

	if len(config) == 0 {
		fmt.Println("No stunnel groups found.")
		return nil
	}

	output, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return E.Cause(err, "marshal config")
	}

	if outputFile != "" {
		err = os.WriteFile(outputFile, output, 0644)
		if err != nil {
			return E.Cause(err, "write output file")
		}
		fmt.Printf("Exported to: %s\n", outputFile)
	} else {
		fmt.Println(string(output))
	}

	fmt.Fprintln(os.Stderr, "Note: Export shows tag names only. For 'stunnel apply', you need full outbound configs.")
	return nil
}