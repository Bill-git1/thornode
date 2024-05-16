package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

////////////////////////////////////////////////////////////////////////////////////////
// Notify
////////////////////////////////////////////////////////////////////////////////////////

func Notify(w Webhooks, title string, lines []string, tag bool, fields *OrderedMap) error {
	log.Info().Str("title", title).Msg("sending notifications")

	// if in console mode only print
	if config.Console {
		console(title, lines, fields)
		return nil
	}

	// send slack
	err := Retry(
		config.MaxRetries,
		func() error { return slack(w.Slack, title, lines, tag, fields) },
	)
	if err != nil {
		log.Error().Err(err).Msg("unable to send slack notification")
		return err
	}

	// send discord
	err = Retry(
		config.MaxRetries,
		func() error { return discord(w.Discord, title, lines, tag, fields) },
	)
	if err != nil {
		log.Error().Err(err).Msg("unable to send discord notification")
		return err
	}

	// send pagerduty
	err = Retry(
		config.MaxRetries,
		func() error { return pagerduty(w.PagerDuty, title, lines, fields) },
	)
	if err != nil {
		log.Error().Err(err).Msg("unable to send pagerduty notification")
		return err
	}

	return nil
}

////////////////////////////////////////////////////////////////////////////////////////
// Internal
////////////////////////////////////////////////////////////////////////////////////////

// match markdown links
var reLinkMdToSlack = regexp.MustCompile(`\[(.*?)\]\((.*?)\)`)

// match urls
var reURL = regexp.MustCompile(`https?://\S+`)

func slack(webhook, title string, lines []string, tag bool, fields *OrderedMap) error {
	if title != "" {
		lines = append([]string{fmt.Sprintf("*%s*", title)}, lines...)
	}

	// add fields to the message
	for _, k := range fields.Keys() {
		v, _ := fields.Get(k)
		lines = append(lines, fmt.Sprintf("*%s*: %s", k, v))
	}

	// add tags to the message
	if tag {
		lines = append(lines, "<!here>")
	}

	// format lines of the message as a quote
	for i, line := range lines {
		lines[i] = "> " + line
	}

	// join the lines into a single message
	message := strings.Join(lines, "\n")

	// replace markdown links with slack links
	message = reLinkMdToSlack.ReplaceAllString(message, "<$2|$1>")

	// build the request
	data := map[string]string{
		"text": message,
	}
	body, err := json.Marshal(data)
	if err != nil {
		log.Error().Err(err).Msg("unable to marshal slack message")
		return err
	}

	// send the request
	resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Error().Err(err).Msg("unable to send slack message")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			log.Error().Str("status", resp.Status).Str("body", string(body)).Msg("slack error")
		} else {
			log.Error().Err(err).Str("status", resp.Status).Msg("unable to read slack response")
		}
		return fmt.Errorf("failed to send slack message")
	}

	return nil
}

func discord(webhook, title string, lines []string, tag bool, fields *OrderedMap) error {
	if title != "" {
		lines = append([]string{fmt.Sprintf("*%s*", title)}, lines...)
	}

	// add fields to the message
	for _, k := range fields.Keys() {
		v, _ := fields.Get(k)
		lines = append(lines, fmt.Sprintf("**%s**: %s", k, v))
	}

	// add tags to the message
	if tag {
		lines = append(lines, "@here")
	}

	// format lines of the message as a quote
	for i, line := range lines {
		lines[i] = "> " + line
	}

	// join the lines into a single message
	message := strings.Join(lines, "\n")

	// wrap urls in <> to prevent previews
	message = reURL.ReplaceAllString(message, "<$0>")

	// build the request
	data := map[string]string{
		"content": message,
	}
	body, err := json.Marshal(data)
	if err != nil {
		log.Error().Err(err).Msg("unable to marshal discord message")
		return err
	}

	// send the request
	resp, err := http.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Error().Err(err).Msg("unable to send discord message")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			log.Error().Str("status", resp.Status).Str("body", string(body)).Msg("discord error")
		} else {
			log.Error().Err(err).Str("status", resp.Status).Msg("unable to read discord response")
		}
		return fmt.Errorf("failed to send discord message")
	}

	return nil
}

func console(title string, lines []string, fields *OrderedMap) {
	// ansi escape codes
	boldStart := "\033[1m"
	italicStart := "\033[3m"
	blue := "\033[34m"
	reset := "\033[0m"

	if title != "" {
		lines = append([]string{fmt.Sprintf("%s%s%s", boldStart, title, reset)}, lines...)
	}

	// add fields to the message
	if fields != nil {
		for _, k := range fields.Keys() {
			v, _ := fields.Get(k)
			lines = append(lines, fmt.Sprintf("%s%s%s: %s", italicStart, k, reset, v))
		}
	}

	fmt.Println()
	for _, line := range lines {
		// handle ansi formatting
		for {
			newLine := strings.Replace(line, "**", boldStart, 1)
			newLine = strings.Replace(newLine, "**", reset, 1)
			newLine = strings.Replace(newLine, "`", blue, 1)
			newLine = strings.Replace(newLine, "`", reset, 1)
			if newLine == line {
				break
			}
			line = newLine
		}

		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("--------------------------------------------------")
}

func pagerduty(webhook, title string, lines []string, fields *OrderedMap) error {
	log.Error().Msg("pagerduty not yet implemented")
	return nil
}
