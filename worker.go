package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sort"
	"strings"
	"time"
)

var (
	server_ip_port    string
	measurementsArray []int
	workerport_UDP    = ":8080"
	workerport_RPC    string
	localaddress      string
	client            *rpc.Client // RPC client.
	logger            *log.Logger // Global logger.
	md5HashValue 	string
)

// Resource server type.
type Worker int

type MWebsiteReq struct {
	URI              string // URI of the website to measure
	SamplesPerWorker int    // Number of samples, >= 1
}

// Response to:
// MServer.MeasureWebsite:
//   - latency stats per worker to a *URI*
//   - (optional) Diff map
// MServer.GetWorkers
//   - latency stats per worker to the *server*
type MRes struct {
	Stats map[string]LatencyStats // map: workerIP -> LatencyStats
	//Diff  map[string]map[string]bool // map: [workerIP x workerIP] -> True/False
}

// A stats struct that summarizes a set of latency measurements to an
// internet host.
type LatencyStats struct {
	Min    int // min measured latency in milliseconds to host
	Median int // median measured latency in milliseconds to host
	Max    int // max measured latency in milliseconds to host
}

type WorkerRes struct {
	WorkerIp string
	Min      int
	Median   int
	Max      int
	Md5Value string
}

func main() {

	args := os.Args[1:]
	done := make(chan int)

	// Missing command line args.
	if len(args) != 1 {
		fmt.Println("Usage: go run server.go [worker-incoming ip:port] [client-incoming ip:port]")
		return
	}

	server_ip_port = args[0]
	workerport_RPC = splitIpToGetPort(server_ip_port)
	fmt.Println("workerport_RPC", workerport_RPC)
	fmt.Println("\nMain funtion: commandline args check server_ip_port: ", server_ip_port, "\n")

	GetOutboundIP() // get local ip

	client = getRPCClientWorker() // Create RPC client for contacting the server.
	sendWorkerIptoServer(client)  // send local ip to server to store

	go InitServerWorkerUDP()

	<-done
}

// fetch the requested page
func fetchPage(uri string, numberoftimes int) []int {
	fmt.Println("\nfetchPage called\n")
	fmt.Println("\nfetchPage uri", uri, "#oftimes", numberoftimes)

	var diffToInt int
	// timing the request to uri
	for i := 0; i < numberoftimes; i++ {

		start := time.Now()
		// getting the uri response
		response, err := http.Get(uri)
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			end := time.Now()
			diff := end.Sub(start)
			diffToSec := diff.Seconds()
			diffToSec = diffToSec * 1000
			diffToInt = int(diffToSec)
			log.Printf("\nfetchPage took %s", diffToInt)
			fmt.Println("statuscode:", response.StatusCode)
		}
		if err != nil {
			fmt.Println(err)
		} else {
			response.Body.Close()
			//_, err := io.Copy(os.Stdout, response.Body)
			if err != nil {
				fmt.Println(err)
			}
		}

		md5HashValue = getMD5Hash(response.Body)
		fmt.Println("md5hash", md5HashValue)

		measurementsArray = append(measurementsArray, diffToInt)
	}

	fmt.Println("\nmeasurementsArray", measurementsArray, "\n")

	return measurementsArray
}

func checkError(msg string, err error, exit bool) {
	if err != nil {
		log.Println(msg, err)
		if exit {
			os.Exit(-1)
		}
	}
}

func computeMin(array []int) int {
	sort.Ints(array)
	fmt.Println("\nmin number in array: ", array[0], "\n")
	return array[0]
}

func computeMax(array []int) int {
	sort.Ints(array)
	i := len(array) - 1
	fmt.Println("max number in array: ", array[i], "\n")
	return array[i]
}

func computeMedian(array []int) int {
	var median int
	sort.Ints(array)
	middle := ((len(array)) / 2)
	if len(array)%2 == 0 {
		medianA := array[middle]
		medianB := array[middle-1]
		median = (medianA + medianB) / 2
	} else {
		median = array[middle+1]
	}
	fmt.Println("\nmedian number in array: ", median, "\n")
	return median
}

// Get preferred outbound ip of this machine
func GetOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().String()
	idx := strings.LastIndex(localAddr, ":")
	localaddress = localAddr[0:idx]
	fmt.Println("\nGetOutboundIP:", localaddress, "\n")
	return localaddress
}

// Create RPC client for contacting the server.
func getRPCClientWorker() *rpc.Client {
	fmt.Println("getRPCClientWorker called")
	raddr, err := net.ResolveTCPAddr("tcp", server_ip_port)
	fmt.Println("workerport_RPC", workerport_RPC)
	fmt.Println("server_ip_port", server_ip_port)
	fmt.Println("raddr", raddr)
	fmt.Println("server_ip_port", server_ip_port)
	if err != nil {
		fmt.Println("err", err)
		logger.Fatal(err)
	}
	conn, err := net.DialTCP("tcp", nil, raddr)
	fmt.Println("conn", conn)
	if err != nil {
		fmt.Println("err", err)
		logger.Fatal(err)
	}
	client := rpc.NewClient(conn)

	return client
}

// sends local ip to server
func sendWorkerIptoServer(client *rpc.Client) (bool, error) {
	fmt.Println("\nsendWorkerIptoServer called:\n")
	var reply bool
	var err error
	fmt.Println("\nlocaladdress:", localaddress, "\n")
	err = client.Call("Worker.ReceiveWorkerIp", localaddress, &reply) // send localaddress to the server to store
	checkError("", err, false)

	client.Close() // close the client, so we can start listening

	go InitWorkerServerRPC() // start listening
	return reply, err
}

func InitWorkerServerRPC() {
	wServer := rpc.NewServer()
	w := new(Worker)
	wServer.Register(w)

	ip := localaddress + ":" + workerport_RPC
	fmt.Println("InitWorkerServerRPC ip", ip)
	l, err := net.Listen("tcp", ip)
	checkError("InitWorkerServerRPC", err, false)
	for {
		fmt.Println("for loop")
		conn, err := l.Accept()
		checkError("InitWorkerServerRPC", err, false)
		go wServer.ServeConn(conn)
	}
}

func (w *Worker) GetWeb(m MWebsiteReq, reply *WorkerRes) error {
	fmt.Println("\n getWeb called\n")
	fmt.Println("\n getWeb uri\n", m.URI)
	array := fetchPage(m.URI, m.SamplesPerWorker)
	min := computeMin(array)
	median := computeMedian(array)
	max := computeMax(array)
	ip := localaddress

	r := WorkerRes{
		WorkerIp: ip,
		Min:      min,
		Median:   median,
		Max:      max,
		Md5Value: md5HashValue,
	}
	fmt.Println("getweb ip,min,median,max:", ip, min, median, max, md5HashValue)
	*reply = r
	return nil
}

func splitIpToGetPort(ip string) string {
	fmt.Println("splitIpToGetPort called")
	s := strings.Split(ip, ":")
	return s[1]
}

// initialize worker to listen for server
func InitServerWorkerUDP() {
	fmt.Println("InitServerWorkerUDP CALLED localaddress", localaddress+workerport_UDP)
	udpAddr, err := net.ResolveUDPAddr("udp", localaddress+workerport_UDP)
	//udpAddr, err := net.ResolveUDPAddr("udp", "128.189.116.114:8080")
	fmt.Println("InitServerWorkerUDP CALLED udpAddr", udpAddr)
	checkError("udp1", err, false)

	conn, err := net.ListenUDP("udp", udpAddr)
	checkError("udp2", err, false)

	for {
		handle(conn)
	}
}

func handle(conn *net.UDPConn) {

	var buf [512]byte

	n, addr, err := conn.ReadFromUDP(buf[0:])
	if err != nil {
		return
	}
	fmt.Println(string(buf[0:n]))

	if string(buf[0:n]) == "ping" {
		msg := "pong"
		conn.WriteToUDP([]byte(msg), addr)
	}

}

func getMD5Hash(file io.ReadCloser) string {
	buf := new(bytes.Buffer)
	buf.ReadFrom(file)
	newStr := buf.String()

	hash := md5.Sum([]byte(newStr))
	return hex.EncodeToString(hash[:])

}
