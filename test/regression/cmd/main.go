package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"text/template"

	"github.com/rs/zerolog/log"
)

////////////////////////////////////////////////////////////////////////////////////////
// Main
////////////////////////////////////////////////////////////////////////////////////////

func main() {
	cleanExports()

	// parse the regex in the RUN environment variable to determine which tests to run
	runRegex := regexp.MustCompile(".*")
	if len(os.Getenv("RUN")) > 0 {
		runRegex = regexp.MustCompile(os.Getenv("RUN"))
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

		if runRegex.MatchString(path) {
			files = append(files, path)
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
	if envParallelism != "" && os.Getenv("DEBUG") == "" {
		parallelism, err = strconv.Atoi(envParallelism)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to parse PARALLELISM")
		}
	} else if envParallelism != "" && os.Getenv("DEBUG") != "" {
		log.Warn().Msg("PARALLELISM is not supported in DEBUG mode, ignoring parallelism value")
	}

	newRegressionTest(files, parallelism).run()
}
