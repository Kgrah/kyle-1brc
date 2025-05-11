package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

const haidarnagar = "Haidarnagar"
const tokyo = "Tokyo"
const measurementPath = "measurements.txt"

var billion = 1000000000

func main() {
	start := time.Now()

	numChunks := 16

	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("starting 1brc with", runtime.NumCPU(), "threads")

	chunks, err := getChunkPositions(numChunks)
	if err != nil {
		fmt.Println("error getting chunk positions:", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(numChunks)

	statsChan := make(chan *stats, numChunks)
	for _, c := range chunks {
		go func(c []int64) {
			defer wg.Done()

			s := &stats{
				data:   make(map[uint64]*measurement),
				cities: make(map[uint64]string),
			}

			err := processChunk(s, measurementPath, c, &wg)
			if err != nil {
				fmt.Println("error processing chunk: ", err)
				return
			}

			s.finalize()
			statsChan <- s
		}(c)
	}

	chosenCity := tokyo

	wg.Wait()
	close(statsChan)

	var out []*stats
	for s := range statsChan {
		out = append(out, s)
	}

	merged := &stats{
		data:   make(map[uint64]*measurement),
		cities: make(map[uint64]string),
	}
	mergeStats(merged, out)

	merged.print(chosenCity)
	fmt.Println("final elapsed time: ", time.Since(start))
}

func processChunk(s *stats, fp string, c []int64, wg *sync.WaitGroup) error {
	f, err := os.Open(fp)
	if err != nil {
		return fmt.Errorf("error processing chunk: %v. err: %w", c, err)
	}
	defer f.Close()

	f.Seek(c[0], io.SeekStart)
	r := bufio.NewReader(f)

	var pos = c[0]
	for {
		if pos >= c[1] {
			break
		}

		l, err := r.ReadSlice('\n')
		if err != nil {
			if err == io.EOF {
				break
			}

			return fmt.Errorf("error finding line in chunk: %w", err)
		}

		pos += int64(len(l))
		l = bytes.TrimRight(l, "\n")

		c, t, b := splitMeasurement(l)
		if c == 0 {
			panic("bad city in line")
		}

		s.process(c, t, b)
	}

	return nil
}

type measurement struct {
	min, max float64
	sum      float64
	c        float64
	mean     float64
}

type stats struct {
	data   map[uint64]*measurement
	cities map[uint64]string
}

func (s *stats) print(c string) {
	if c != "" {
		cHash := polyHash([]byte(c))
		if m, ok := s.data[cHash]; ok {
			fmt.Printf("%s=%.1f/%.1f/%.1f/%.1f\n", c, m.min, m.mean, m.max, m.c)
		}

		return
	}

	for _, v := range s.cities {
		cHash := polyHash([]byte(v))
		c := s.cities[cHash]
		m := s.data[cHash]
		fmt.Printf("%s=%.1f/%.1f/%.1f\n", c, m.min, m.mean, m.max)
	}
}

func (s *stats) process(c uint64, t float64, b []byte) {
	if m, ok := s.data[c]; ok {
		if t > m.max {
			m.max = t
		}

		if t < m.min {
			m.min = t
		}

		m.sum += t
		m.c++
	} else {
		s.data[c] = &measurement{
			min:  t,
			max:  t,
			sum:  t,
			c:    1,
			mean: 0,
		}

		s.cities[c] = string(b)
	}
}

func (s *stats) finalize() {
	for _, m := range s.data {
		if m.c != 0 {
			m.mean = m.sum / m.c
		}
	}
}

func splitMeasurement(line []byte) (uint64, float64, []byte) {
	for i := range len(line) {
		if line[i] == ';' {
			c, tBytes := line[:i], line[i+1:]
			t := fastParseFloat(tBytes)
			return polyHash(c), t, c
		}
	}

	return 0, 0, nil
}

func getChunkPositions(n int) ([][]int64, error) {
	f, err := os.Open(measurementPath)
	if err != nil {
		return nil, fmt.Errorf("error getting file handle: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("error getting file stat: %w", err)
	}

	size := info.Size()
	approxChunkSize := size / int64(n)

	chunks := make([][]int64, 0, n)
	var start int64

	for i := range n {
		var end int64
		if i == n-1 {
			end = size
		} else {
			end = start + approxChunkSize

			f.Seek(end, io.SeekStart)
			reader := bufio.NewReader(f)
			l, err := reader.ReadString('\n')
			if err != nil {
				return nil, fmt.Errorf("error finding line %w", err)
			}

			end += int64(len(l))
		}

		chunks = append(chunks, []int64{start, end})
		start = end
	}

	return chunks, nil
}

func fastParseFloat(b []byte) float64 {
	var intPart, fracPart int64
	var fracDiv float64 = 1

	i := 0
	// Integer part
	for ; i < len(b) && b[i] != '.'; i++ {
		intPart = intPart*10 + int64(b[i]-'0')
	}

	// Fractional part
	if i < len(b) && b[i] == '.' {
		i++
		for ; i < len(b); i++ {
			fracPart = fracPart*10 + int64(b[i]-'0')
			fracDiv *= 10
		}
	}

	result := float64(intPart) + float64(fracPart)/fracDiv
	return result
}

func mergeStats(merged *stats, s []*stats) {
	for _, s := range s {
		for c, m := range s.data {
			if mrg, ok := merged.data[c]; ok {
				mrg.sum += m.sum
				mrg.c += m.c
				mrg.max = max(mrg.max, m.max)
				mrg.min = min(mrg.min, m.min)
			} else {
				merged.data[c] = m
			}
		}

		for ch, c := range s.cities {
			if _, ok := merged.cities[ch]; ok {
				continue
			}

			merged.cities[ch] = c
		}
	}
}

func polyHash(b []byte) uint64 {
	var h uint64
	for _, c := range b {
		h = h*31 + uint64(c)
	}
	return h
}
