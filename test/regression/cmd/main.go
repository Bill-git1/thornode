package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/rs/zerolog/log"
)

////////////////////////////////////////////////////////////////////////////////////////
// Main
////////////////////////////////////////////////////////////////////////////////////////

func main() {
	cleanExports()

	// parse the regex in the RUN environment variable to determine which tests to run
	var runRegexs []*regexp.Regexp
	if len(os.Getenv("RUN")) > 0 {
		csv := strings.Split(os.Getenv("RUN"), ",")
		for _, item := range csv {
			item = strings.Trim(item, " \"")
			if len(item) == 0 {
				continue
			}
			runRegexs = append(runRegexs, regexp.MustCompile(item))
		}
	} else {
		runRegexs = append(runRegexs, regexp.MustCompile(".*"))
	}

	// find all regression tests in path
	files := []string{}
	err := filepath.Walk("suites", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// skip files that are not yaml
		if filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml" {
			return nil
		}

		for _, r := range runRegexs {
			if r.MatchString(path) {
				files = append(files, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to find regression tests")
	}

	// sort the files descending by the number of blocks created (so long tests run first)
	counts := make(map[string]int)
	for _, file := range files {
		ops, _, _, _ := parseOps(log.Output(io.Discard), file, template.Must(templates.Clone()), []string{})
		counts[file] = blockCount(ops)
	}
	sort.Slice(files, func(i, j int) bool {
		return counts[files[i]] > counts[files[j]]
	})

	// get parallelism from environment variable if DEBUG is not set
	parallelism := 1
	envParallelism := os.Getenv("PARALLELISM")
	if envParallelism != "" {
		if os.Getenv("DEBUG") != "" {
			log.Fatal().Msg("PARALLELISM is not supported in DEBUG mode")
		}
		parallelism, err = strconv.Atoi(envParallelism)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to parse PARALLELISM")
		}
	}

	newRegressionTest(files, parallelism).run()
}
