package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

const haidarnagar = "Haidarnagar"
const tokyo = "Tokyo"

var billion = 1000000000

var totalProcessed int32

func main() {
	start := time.Now()

  measurementPath := "measurements.txt"
  numChunks := 16 

	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("starting 1brc with", runtime.NumCPU(), "threads")

	f, err := os.Open(measurementPath)
	if err != nil {
		fmt.Println("error opening measurements file: ", err)
		return
	}

	chunks, err := getChunkPositions(numChunks, f)
	if err != nil {
		fmt.Println("error getting chunk positions:", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(numChunks)

  statsChan := make(chan *stats, numChunks)
	for _, c := range chunks {
		go func(c []int64) {
	    s := &stats{
        data: make(map[string]*measurement),
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

	go func() {
		for {
			fmt.Println(time.Since(start))
      progress := float64(totalProcessed)/float64(billion)*100
      fmt.Println("progress: ", progress, "%")
			time.Sleep(time.Second * 5)
      if progress == 100 {
        return
      } 
		}
	}()


	wg.Wait()
  close(statsChan)

  var out []*stats
  for s := range statsChan {
    out = append(out, s)
  }

  merged := &stats{
    data: make(map[string]*measurement),
  }
  mergeStats(merged, out)

	merged.print(chosenCity)
	fmt.Println("final elapsed time: ", time.Since(start))
}

func processChunk(s *stats, fp string, c []int64, wg *sync.WaitGroup) error {
	defer wg.Done()

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

		c, t, err := splitMeasurement(string(l))
		if err != nil {
			return fmt.Errorf("error splitting measurement %w", err)
		}

		s.process(c, t)
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
	data map[string]*measurement
}

func (s *stats) print(c string) {
	if c != "" {
    if m, ok := s.data[c]; ok {
			fmt.Printf("%s=%.1f/%.1f/%.1f/%.1f\n", c, m.min, m.mean, m.max, m.c)
    }

		return
	}

  for c, m := range s.data {
		fmt.Printf("%s=%.1f/%.1f/%.1f\n", c, m.min, m.mean, m.max)
  }
}

func (s *stats) process(c string, t float64) {
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
      min: t,
      max: t,
      sum: t,
      c: 1,
      mean: 0,
    }
  }

	atomic.AddInt32(&totalProcessed, 1)
}

func (s *stats) finalize() {
  for _, m := range s.data {
    if m.c != 0 {
      m.mean = m.sum / m.c
    }
  }
}

func splitMeasurement(line string) (string, float64, error) {
	for i := range len(line) {
		if line[i] == ';' {
			c, tString := line[:i], line[i+1:]
      t := fastParseFloat(tString)
			return c, t, nil
		}
	}

	return "", 0, fmt.Errorf("error splitting line: no line break found")
}

func getChunkPositions(n int, f *os.File) ([][]int64, error) {
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

func fastParseFloat(s string) float64 {
	var neg bool
	var intPart, fracPart int64
	var fracDiv float64 = 1

	// Convert string to byte slice for faster indexing
	b := []byte(s)
	i := 0

	// Optional sign
	if b[0] == '-' {
		neg = true
		i++
	}

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
	if neg {
		result = -result
	}
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
  }
}

