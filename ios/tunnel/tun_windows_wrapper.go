//go:build windows

package tunnel

import (
	"fmt"
	"io"
	"os/exec"

	"net/http"
	_ "net/http/pprof"

	"time"

	log "github.com/sirupsen/logrus"
)

type tunWrapper struct {
	device Device

	buffer [][]byte
}

func initTUNwrapper(device Device) *tunWrapper {
	t := &tunWrapper{}

	t.device = device
	if device.BatchSize() != 1 {
		panic("batch size not 1")
	}

	mtu, _ := t.device.MTU()
	log.Infof("batch size: %d mtu:%d", device.BatchSize(), mtu)

	t.buffer = make([][]byte, 1)
	t.buffer[0] = make([]byte, mtu)
	go func() {
		// Create a counter to track events
		eventCount := 0
		// Adaptive sleep duration with exponential backoff
		sleepTime := 50 * time.Millisecond
		maxSleepTime := 500 * time.Millisecond
		minSleepTime := 50 * time.Millisecond
		emptyCounter := 0

		for {
			// Sleep first to reduce CPU usage
			time.Sleep(sleepTime)

			// Drain all pending events
			drainCount := 0
			for i := 0; i < 100; i++ { // Limit max events per cycle to prevent starvation
				// Try to receive event in non-blocking way
				select {
				case _ = <-device.Events():
					// Count drained events
					drainCount++
					eventCount++
				default:
					// No more events, exit the draining loop
					goto doneDraining
				}
			}

		doneDraining:
			// Adjust sleep time based on activity
			if drainCount > 0 {
				// Events were found, reset to minimum sleep time for responsiveness
				sleepTime = minSleepTime
				emptyCounter = 0

				// Log occasionally
				if eventCount%100000 == 0 {
					log.Infof("Processed %d events (total: %d)", drainCount, eventCount)
				}
			} else {
				// No events found, gradually increase sleep time
				emptyCounter++
				if emptyCounter > 5 && sleepTime < maxSleepTime {
					// Increase sleep time after several empty cycles (exponential backoff)
					sleepTime = time.Duration(float64(sleepTime) * 1.5)
					if sleepTime > maxSleepTime {
						sleepTime = maxSleepTime
					}
				}
			}
		}
	}()
	return t
}

func (t *tunWrapper) Close() error {
	return t.device.Close()
}

func (t *tunWrapper) Write(p []byte) (int, error) {

	bufs := [][]byte{p}                     // Create a slice of one byte slice
	written, err := t.device.Write(bufs, 0) // Use offset 0
	if written > 0 {
		return len(p), err // Assume the entire slice was written
	}
	return 0, err
}

func (t *tunWrapper) Read(p []byte) (int, error) {

	sizes := make([]int, 1)
	_, err := t.device.Read(t.buffer, sizes, 0)

	if err != nil {
		return 0, err
	}

	buf := t.buffer[0]
	size := sizes[0]
	copy(p, buf[:size])
	return size, err

}

func setupWindowsTUN(tunnelInfo tunnelParameters) (io.ReadWriteCloser, error) {
	name := "tun0"

	tunDevice, err := CreateTUN(name, int(tunnelInfo.ClientParameters.Mtu))
	if err != nil {
		fmt.Println("Error creating TUN device:", err)
		return &tunWrapper{}, err
	}
	tunname, err := tunDevice.Name()

	if err != nil {
		return nil, fmt.Errorf("setupTunnelInterface: failed to get interface name: %w", err)
	}
	const prefixLength = 64
	setIpAddr := exec.Command("netsh", "interface", "ipv6", "set", "address", tunname, fmt.Sprintf("%s/%d", tunnelInfo.ClientParameters.Address, prefixLength))
	err = runCmd(setIpAddr)
	if err != nil {
		return nil, fmt.Errorf("setupTunnelInterface: failed to set IP address for interface: %w", err)
	}
	log.Info("windows cmd")
	log.Info(setIpAddr.String())

	return initTUNwrapper(tunDevice), nil
}

func init() {
	go func() {
		http.ListenAndServe("localhost:6060", nil)
	}()
}
