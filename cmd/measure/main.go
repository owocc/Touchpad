package main

import (
	"flag"
	"fmt"
	"math"
	"net"
	"sort"
	"time"

	"touchpad/pkg/protocol"
)

func main() {
	target := flag.String("target", "127.0.0.1:29180", "target host UDP address")
	count := flag.Int("count", 1000, "number of probe packets to send")
	intervalMs := flag.Int("interval", 8, "packet sending interval in ms (8ms = 125Hz)")
	flag.Parse()

	fmt.Printf("=== Touchpad Latency & Jitter Measurement Tool ===\n")
	fmt.Printf("Target: %s\nProbes: %d\nRate: %d ms (~%.0f Hz)\n\n",
		*target, *count, *intervalMs, 1000.0/float64(*intervalMs))

	conn, err := net.Dial("udp", *target)
	if err != nil {
		fmt.Printf("Error dialing target: %v\n", err)
		return
	}
	defer conn.Close()

	rtts := make([]float64, 0, *count)
	lost := 0
	buf := make([]byte, 256)

	pkt := protocol.Packet{
		Version: ProtocolVersionByte(),
		Type:    protocol.MsgPing,
	}

	for i := 0; i < *count; i++ {
		pkt.SequenceNumber = uint32(i + 1)
		now := time.Now()
		pkt.TimestampMicros = uint32(now.UnixMicro())

		data := pkt.Encode(nil)
		if _, err := conn.Write(data); err != nil {
			lost++
			continue
		}

		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := conn.Read(buf)
		if err != nil {
			lost++
			continue
		}

		var resp protocol.Packet
		if err := resp.Decode(buf[:n]); err != nil || resp.Type != protocol.MsgPong {
			lost++
			continue
		}

		rtt := float64(time.Since(now).Microseconds()) / 1000.0 // ms
		if rtt >= 0 && rtt < 1000 {
			rtts = append(rtts, rtt)
		} else {
			lost++
		}

		time.Sleep(time.Duration(*intervalMs) * time.Millisecond)
	}

	if len(rtts) == 0 {
		fmt.Println("No probe responses received! Is the host running?")
		return
	}

	sort.Float64s(rtts)
	n := len(rtts)
	p50 := rtts[int(float64(n)*0.50)]
	p95 := rtts[int(float64(n)*0.95)]
	p99 := rtts[int(float64(n)*0.99)]

	var sum float64
	for _, v := range rtts {
		sum += v
	}
	mean := sum / float64(n)

	var variance float64
	for _, v := range rtts {
		variance += (v - mean) * (v - mean)
	}
	stdDev := math.Sqrt(variance / float64(n))

	fmt.Printf("--- Measurement Results ---\n")
	fmt.Printf("Packets Sent:     %d\n", *count)
	fmt.Printf("Packets Received: %d (Loss: %.2f%%)\n", n, float64(lost)/float64(*count)*100.0)
	fmt.Printf("Min Latency:      %.3f ms\n", rtts[0])
	fmt.Printf("Mean Latency:     %.3f ms (StdDev: %.3f ms)\n", mean, stdDev)
	fmt.Printf("p50 (Median):     %.3f ms\n", p50)
	fmt.Printf("p95:              %.3f ms\n", p95)
	fmt.Printf("p99:              %.3f ms\n", p99)
	fmt.Printf("Max Latency:      %.3f ms\n", rtts[n-1])
}

func ProtocolVersionByte() uint8 {
	return protocol.ProtocolVersion
}
