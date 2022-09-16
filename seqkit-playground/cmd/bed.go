// Copyright © 2016-2019 Wei Shen <shenwei356@gmail.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/shenwei356/breader"
)

// BedFeature is the gff BedFeature struct
type BedFeature struct {
	Chr    string
	Start  int // 1based
	End    int // end included
	Name   *string
	Strand *string
}

// Threads for bread.NewBufferedReader()
var Threads = runtime.NumCPU()

// ReadBedFeatures returns gtf BedFeatures of a file
func ReadBedFeatures(file string) ([]BedFeature, error) {
	return ReadBedFilteredFeatures(file, []string{})
}

// ReadBedFilteredFeatures returns gtf BedFeatures of selected chrs from file
func ReadBedFilteredFeatures(file string, chrs []string) ([]BedFeature, error) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return nil, err
	}
	chrsMap := make(map[string]struct{}, len(chrs))
	for _, chr := range chrs {
		chrsMap[chr] = struct{}{}
	}

	fn := func(line string) (interface{}, bool, error) {
		line = strings.TrimRight(line, "\r\n")

		if line == "" || line[0] == '#' || (len(line) > 7 && string(line[0:7]) == "browser") || (len(line) > 5 && string(line[0:5]) == "track") {
			return nil, false, nil
		}

		items := strings.Split(line, "\t")
		n := len(items)
		if n < 3 {
			return nil, false, nil
		}

		if len(chrs) > 0 { // selected chrs
			if _, ok := chrsMap[items[0]]; !ok {
				return nil, false, nil
			}
		}

		start, err := strconv.Atoi(items[1])
		if err != nil {
			return nil, false, fmt.Errorf("%s: bad start: %s", items[0], items[1])
		}
		end, err := strconv.Atoi(items[2])
		if err != nil {
			return nil, false, fmt.Errorf("%s: bad end: %s", items[0], items[2])
		}
		if start >= end {
			return nil, false, fmt.Errorf("%s: start (%d) must be <= end (%d)", items[0], start, end)
		}

		var name *string
		if n >= 4 {
			name = &items[3]
		}
		var strand *string
		if n >= 6 {
			if items[5] != "+" && items[5] != "-" && items[5] != "." {
				return nil, false, fmt.Errorf("bad strand: %s", items[5])
			}
			strand = &items[5]
		}

		return BedFeature{items[0], start + 1, end, name, strand}, true, nil
	}
	reader, err := breader.NewBufferedReader(file, Threads, 100, fn)
	if err != nil {
		return nil, err
	}
	BedFeatures := make([]BedFeature, 0, 1024)
	for chunk := range reader.Ch {
		if chunk.Err != nil {
			return nil, chunk.Err
		}
		for _, data := range chunk.Data {
			BedFeatures = append(BedFeatures, data.(BedFeature))
		}
	}
	return BedFeatures, nil
}
