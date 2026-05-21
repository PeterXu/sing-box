package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/spf13/cobra"
)

var commandStunnelRemoveForce bool
var commandStunnelUpdateForce bool

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

var commandStunnelAdd = &cobra.Command{
	Use:   "add <group> <outbound...>",
	Short: "Add outbounds to a stunnel group",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		err = stunnelAdd(addr, secret, client, args[0], args[1:])
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

var commandStunnelUpdate = &cobra.Command{
	Use:   "update <group> <outbound...>",
	Short: "Replace outbound list of a stunnel group",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		addr, secret, err := clashAPIConfig()
		if err != nil {
			log.Fatal(err)
		}
		client := &http.Client{Timeout: 30 * time.Second}
		err = stunnelUpdate(addr, secret, client, args[0], args[1:], commandStunnelUpdateForce)
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandStunnelRemove.Flags().BoolVar(&commandStunnelRemoveForce, "force", false, "proceed even if removing active outbound")
	commandStunnelUpdate.Flags().BoolVar(&commandStunnelUpdateForce, "force", false, "proceed even if removing active outbound")
	commandStunnel.AddCommand(commandStunnelList, commandStunnelAdd, commandStunnelRemove, commandStunnelUpdate)
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
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies/"+group, secret, nil)
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
	_, err := clashAPIRequest(client, http.MethodPut, baseURL+"/proxies/"+group+"/members", secret, body)
	return err
}

func getMemberDelay(baseURL, secret string, client *http.Client, tag string) uint16 {
	data, err := clashAPIRequest(client, http.MethodGet, baseURL+"/proxies/"+tag, secret, nil)
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

func stunnelAdd(baseURL, secret string, client *http.Client, group string, tags []string) error {
	info, err := getGroupInfo(baseURL, secret, client, group)
	if err != nil {
		return err
	}
	existing := make(map[string]bool)
	for _, t := range info.All {
		existing[t] = true
	}
	newList := make([]string, len(info.All), len(info.All)+len(tags))
	copy(newList, info.All)
	for _, tag := range tags {
		if existing[tag] {
			fmt.Println("skip existing:", tag)
			continue
		}
		newList = append(newList, tag)
		existing[tag] = true
	}
	if err := setGroupMembers(baseURL, secret, client, group, newList); err != nil {
		return err
	}
	fmt.Println("Done.")
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

func stunnelUpdate(baseURL, secret string, client *http.Client, group string, tags []string, force bool) error {
	info, err := getGroupInfo(baseURL, secret, client, group)
	if err != nil {
		return err
	}
	if info.Now != "" {
		inNew := false
		for _, t := range tags {
			if t == info.Now {
				inNew = true
				break
			}
		}
		if !inNew && !force {
			return E.New(fmt.Sprintf("active outbound %q not in new list. Use --force to proceed.", info.Now))
		}
		if !inNew && force {
			fmt.Fprintf(os.Stderr, "Warning: active outbound %q will be removed, server will re-select.\n", info.Now)
		}
	}
	if err := setGroupMembers(baseURL, secret, client, group, tags); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}
