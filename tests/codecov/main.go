// Copyright 2017 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/golang/glog"
)

var (
	reportFile    = flag.String("report_file", "", "Code coverage report file")
	baselineFile  = flag.String("baseline_file", "", "Code coverage baseline file")
	thresholdFile = flag.String("threshold_file", "", "File containing package to threshold mappings, as overrides")
)

func parseReportLine(line string) (string, float64, error) {
	// <option value="file0">istio.io/istio/galley/cmd/shared/shared.go (0.0%)</option>
	reg := regexp.MustCompile(` *<option value=\"(.*)\">(.*) \((.*)%\)</option>`)
	if m := reg.FindStringSubmatch(line); len(m) != 0 {
		cov, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			return "", 0, err
		}
		return m[2], cov, nil
	}
	return "", 0, fmt.Errorf("no coverage in %s", line)
}

func parseReport(filename string) (map[string]float64, error) {
	coverage := make(map[string]float64)

	f, err := os.Open(filename)
	if err != nil {
		return coverage, fmt.Errorf("failed to open file %s, %v", filename, err)
	}
	defer func() {
		if err = f.Close(); err != nil {
			glog.Warningf("failed to close file %s, %v", filename, err)
		}
	}()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if pkg, cov, err := parseReportLine(scanner.Text()); err == nil {
			coverage[pkg] = cov
		}
	}
	return coverage, scanner.Err()
}

func parseThreshold(thresholdFile string) (map[string]float64, error) {
	f, err := os.Open(thresholdFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open threshold file, %s, %v", thresholdFile, err)
	}
	defer func() {
		if err = f.Close(); err != nil {
			glog.Errorf("failed to close file %s, %v", thresholdFile, err)
		}
	}()

	scanner := bufio.NewScanner(f)
	reg := regexp.MustCompile(`(.*)=(.*)`)

	thresholds := make(map[string]float64)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			// Skip comments
			continue
		}
		m := reg.FindStringSubmatch(line)
		if len(m) == 3 {
			threshold, err := strconv.ParseFloat(strings.TrimSpace(m[2]), 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse threshold to float64 for package %s: %s, %v",
					m[1], m[2], err)
			}
			thresholds[strings.TrimSpace(m[1])] = threshold
		}
	}
	return thresholds, scanner.Err()
}

func findDelta(report, baseline map[string]float64) map[string]float64 {
	deltas := make(map[string]float64)

	for pkg, cov := range report {
		deltas[pkg] = cov - baseline[pkg]
	}
	// Find the remaining packages that exist in baseline but not in report.
	for pkg, base := range baseline {
		if _, exist := report[pkg]; !exist {
			deltas[pkg] = 0 - base
		}
	}
	return deltas
}

func checkDelta(deltas, report, baseline, thresholds map[string]float64) bool {
	result := true
	// First print all coverage change.
	for pkg, delta := range deltas {
		glog.Infof("Coverage change: %s:%f%% (%f%% to %f%%)", pkg, delta, baseline[pkg], report[pkg])
	}

	// Then generate errors for reduced coverage.
	for pkg, delta := range deltas {
		if delta+getThreshold(thresholds, pkg) < 0 {
			glog.Errorf("Coverage dropped: %s:%f%% (%f%% to %f%%)", pkg, delta, baseline[pkg], report[pkg])
			result = false
		}
	}
	return result
}

func getThreshold(thresholds map[string]float64, path string) float64 {
	matchedThreshold := 0.0
	matchedPackageLebgth := 0
	for pkg, threshold := range thresholds {
		// Find the threshold that matches the longest package prefix.
		if strings.HasPrefix(path, pkg) && len(pkg) > matchedPackageLebgth {
			matchedPackageLebgth = len(pkg)
			matchedThreshold = threshold
		}
	}
	return matchedThreshold
}

func checkCoverage(reportFile, baselineFile, thresholdFile string) error {
	report, err := parseReport(reportFile)
	if err != nil {
		return fmt.Errorf("cannot open or parse report file: %s, %v", reportFile, err)
	}
	baseline, err := parseReport(baselineFile)
	if err != nil {
		return fmt.Errorf("cannot open or parse baseline file: %s, %v", baselineFile, err)
	}
	thresholds, err := parseThreshold(thresholdFile)
	if err != nil {
		return fmt.Errorf("cannot open or parse threshold file: %s, %v", thresholdFile, err)
	}
	deltas := findDelta(report, baseline)

	if !checkDelta(deltas, report, baseline, thresholds) {
		return errors.New("some test coverage has dropped more than the allowed threshold")
	}
	return nil
}

// This takes codecov reports generated from PR HEAD abd base and generates errors in case
// code coverage has dropped above the given threshold.
func main() {
	flag.Parse()
	err := checkCoverage(*reportFile, *baselineFile, *thresholdFile)
	if err != nil {
		glog.Error(err)
		os.Exit(1)
	}
	os.Exit(0)
}
