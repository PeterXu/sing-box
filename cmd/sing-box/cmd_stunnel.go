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
		err = stunnelApply(addr, secret, client, args[0], commandStunnelApplyForce)
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
		removeSet[t] = true
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

func stunnelApply(baseURL, secret string, client *http.Client, configFile string, force bool) error {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return E.Cause(err, "read config file")
	}
	var config map[string]stunnelGroupConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return E.Cause(err, "parse config file")
	}
	for group, cfg := range config {
		fmt.Printf("Applying config for group: %s\n", group)
		var tags []string
		if len(cfg.Outbounds) > 0 {
			info, err := getGroupInfo(baseURL, secret, client, group)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Error getting group info: %v\n", err)
				continue
			}
			existing := make(map[string]bool)
			for _, t := range info.All {
				existing[t] = true
			}
			// Process each outbound (can be tag string or full config object)
			for _, outboundRaw := range cfg.Outbounds {
				// Try to parse as string (tag)
				var tagStr string
				if err := json.Unmarshal(outboundRaw, &tagStr); err == nil {
					// It's a tag reference
					_, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies/"+url.PathEscape(tagStr), secret, nil)
					if err != nil {
						fmt.Fprintf(os.Stderr, "  Error: outbound %s not found\n", tagStr)
						continue
					}
					tags = append(tags, tagStr)
				} else {
					// It's a full config object - create outbound
					var rawConfig map[string]interface{}
					if err := json.Unmarshal(outboundRaw, &rawConfig); err != nil {
						fmt.Fprintf(os.Stderr, "  Error parsing outbound config: %v\n", err)
						continue
					}
					tag, ok := rawConfig["tag"].(string)
					if !ok {
						fmt.Fprintf(os.Stderr, "  Error: outbound config missing 'tag' field\n")
						continue
					}
					// Create outbound via API
					_, err := clashAPIRequest(client, http.MethodPost, baseURL+"/proxies", secret, rawConfig)
					if err != nil {
						fmt.Fprintf(os.Stderr, "  Error creating outbound %s: %v\n", tag, err)
						continue
					}
					fmt.Printf("  Created outbound: %s\n", tag)
					tags = append(tags, tag)
				}
			}
			// Check if current active is in new list
			if info.Now != "" {
				inNew := false
				for _, t := range tags {
					if t == info.Now {
						inNew = true
						break
					}
				}
				if !inNew {
					if !force {
						fmt.Fprintf(os.Stderr, "  Error: active outbound %q not in new list. Use --force to proceed.\n", info.Now)
						continue
					}
					fmt.Fprintf(os.Stderr, "  Warning: active outbound %q not in new list, server will re-select.\n", info.Now)
				}
			}
			if len(tags) > 0 {
				if err := setGroupMembers(baseURL, secret, client, group, tags); err != nil {
					fmt.Fprintf(os.Stderr, "  Error updating outbounds: %v\n", err)
					continue
				}
				fmt.Printf("  Updated outbounds: %v\n", tags)
			}
		}
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
			// Export outbounds as tag strings
			var outbounds []json.RawMessage
			for _, tag := range info.All {
				tagJSON, _ := json.Marshal(tag); outbounds = append(outbounds, json.RawMessage(tagJSON))
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
	return nil
}