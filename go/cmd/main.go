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

	var sm sync.Map

	s := stats{
		data: &sm,
	}

	var wg sync.WaitGroup
	wg.Add(numChunks)

	for _, c := range chunks {
		go func() {
			err := processChunk(&s, measurementPath, c, &wg)
			if err != nil {
				fmt.Println("error processing chunk: ", err)
				return
			}
		}()
	}

  chosenCity := tokyo

	go func() {
		for {
			fmt.Println(time.Since(start))
			time.Sleep(time.Second * 5)
			fmt.Println("progress: ", float64(totalProcessed)/float64(billion)*100, "%")
			s.print(chosenCity)
		}
	}()

	wg.Wait()

	s.finalize()
	s.print(chosenCity)
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

		l = bytes.TrimRight(l, "\n")

		pos += int64(len(l))

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
	data *sync.Map
}

func (s *stats) print(c string) {
	if c != "" {
		if m, ok := s.data.Load(c); ok {
			measurement := m.(*measurement)
			fmt.Printf("%s=%.1f/%.1f/%.1f/%.1f\n", c, measurement.min, measurement.mean, measurement.max, measurement.c)
		}
		return
	}

	s.data.Range(func(key, value interface{}) bool {
		measurement := value.(*measurement)
		fmt.Printf("%s=%.1f/%.1f/%.1f\n", key, measurement.min, measurement.mean, measurement.max)
		return true
	})
}

func (s *stats) process(c string, t float64) {
	m, loaded := s.data.Load(c)

	if loaded {
		measurement := m.(*measurement)

		if t > measurement.max {
			measurement.max = t
		}
		if t < measurement.min {
			measurement.min = t
		}

		measurement.sum += t
		measurement.c++
	} else {
		s.data.Store(c, &measurement{
			min:  t,
			max:  t,
			sum:  t,
			c:    1,
			mean: 0, // will be set later in finalize()
		})
	}

	atomic.AddInt32(&totalProcessed, 1)
}

func (s *stats) finalize() {
	s.data.Range(func(key, value interface{}) bool {
		measurement := value.(*measurement)
		if measurement.c != 0 {
			measurement.mean = measurement.sum / measurement.c
		}
		return true
	})
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

