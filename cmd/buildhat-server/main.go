package main

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/alecthomas/kong"
	expect "github.com/google/goexpect"
	"github.com/stianeikeland/go-rpio/v4"
	"github.com/wolfeidau/raspberrypi-buildhat-golang/firmware"
	"go.bug.st/serial"
	"golang.org/x/exp/slices"
)

const (
	buildHATResetPin = 4
	buildHATBootPin  = 22
)

var (
	cli struct {
		Reset struct{} `cmd:"" help:"Reset BuildHAT."`
		Start struct {
			Port string `kong:"required"`
		} `cmd:"" help:"Start the BuildHAT."`
	}

	promptRE = regexp.MustCompile("BHBL>")
	// versionRE = regexp.MustCompile(`BuildHAT bootloader version \d+ [\d\-T:]+`)
	versionLineRE = regexp.MustCompile(`BuildHAT.*\r`)
)

func main() {
	cliCtx := kong.Parse(&cli)

	var err error
	switch cliCtx.Command() {
	case "start":
		err = start(cli.Start.Port)
	case "reset":
		err = reset()
	}
	if err != nil {
		log.Fatal(err)
	}
}

func start(portName string) error {
	err := validatePort(portName)
	if err != nil {
		return err
	}

	port, err := openPort(portName)
	if err != nil {
		return err
	}

	exp, _, err := serialSpawn(port, 10*time.Second, expect.Verbose(true))
	if err != nil {
		return err
	}

	exp.Send("\r")

	_, _, err = exp.Expect(promptRE, 10*time.Second)
	if err != nil {
		return err
	}

	exp.Send("version\r")
	result, _, err := exp.Expect(versionLineRE, 10*time.Second)
	if err != nil {
		return err
	}
	log.Println(result)

	exp.Send("clear\r")

	err = loadFirmware(port, exp)
	if err != nil {
		return err
	}

	_, _, err = exp.Expect(promptRE, 10*time.Second)
	if err != nil {
		return err
	}

	return exp.Send("reboot\r")
}

func reset() error {
	err := rpio.Open()
	if err != nil {
		return err
	}

	log.Println("starting reset")

	resetPin := rpio.Pin(buildHATResetPin)
	resetPin.Output()

	bootPin := rpio.Pin(buildHATBootPin)
	bootPin.Output()

	bootPin.Low()
	resetPin.Low()

	time.Sleep(100 * time.Millisecond)
	resetPin.High()
	time.Sleep(100 * time.Millisecond)

	log.Println("reset complete")

	return nil
}

func loadFirmware(port serial.Port, exp expect.Expecter) error {
	firmwareData, err := firmware.Content.ReadFile("data/firmware.bin")
	if err != nil {
		return err
	}

	signatureData, err := firmware.Content.ReadFile("data/signature.bin")
	if err != nil {
		return err
	}

	_, _, err = exp.Expect(promptRE, 10*time.Second)
	if err != nil {
		return err
	}

	exp.Send(fmt.Sprintf("load %d %d\r", len(firmwareData), checksum(firmwareData)))

	time.Sleep(100 * time.Millisecond)

	port.Write([]byte{0x02})
	port.Write(firmwareData)
	port.Write([]byte{0x03})

	_, _, err = exp.Expect(promptRE, 10*time.Second)
	if err != nil {
		return err
	}

	exp.Send(fmt.Sprintf("signature %d\r", len(signatureData)))

	time.Sleep(100 * time.Millisecond)

	port.Write([]byte{0x02})
	port.Write(signatureData)
	port.Write([]byte{0x03})

	return nil
}

func checksum(data []byte) uint {
	var check uint = 1
	for i := 0; i < len(data); i++ {
		if (check & 0x80000000) != 0 {
			check = (check << 1) ^ 0x1d872b41
		} else {
			check = check << 1
		}

		check = (check ^ uint(data[i])) & 0xFFFFFFFF
	}

	return check
}

func openPort(portName string) (serial.Port, error) {
	mode := &serial.Mode{
		BaudRate: 115200,
	}

	return serial.Open(portName, mode)
}

func serialSpawn(port serial.Port, timeout time.Duration, opts ...expect.Option) (expect.Expecter, <-chan error, error) {

	resCh := make(chan error)

	return expect.SpawnGeneric(&expect.GenOptions{
		In:  port,
		Out: port,
		Wait: func() error {
			return <-resCh
		},
		Close: func() error {
			close(resCh)
			return port.Close()
		},
		Check: func() bool { return true },
	}, timeout, opts...)
}

func validatePort(port string) error {
	ports, err := serial.GetPortsList()
	if err != nil {
		return fmt.Errorf("failed to get port list: %w", err)
	}

	if len(ports) == 0 {
		return errors.New("No serial ports found!")
	}

	if !slices.Contains(ports, port) {
		return fmt.Errorf("Port not found: %s", port)
	}

	return nil
}
