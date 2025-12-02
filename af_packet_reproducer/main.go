package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

// AF_PACKET Socket Issue Reproducer
// Based on: https://github.com/mrVectorz/randoms/tree/master/af_packet_issue
// Reference: RHEL Bug https://issues.redhat.com/browse/RHEL-83393
//
// This program creates multiple AF_PACKET sockets to demonstrate
// the kernel performance degradation issue when many af_packet 
// sockets are open simultaneously.

var (
	socketCount     = flag.Int("sockets", 200, "Number of af_packet sockets to create")
	captureDir      = flag.String("dir", "/data/pcaps", "Directory for packet captures")
	interface_      = flag.String("interface", "any", "Network interface to capture on")
	duration        = flag.Int("duration", 0, "Duration in seconds (0 = infinite)")
	verbose         = flag.Bool("verbose", false, "Enable verbose output")
	metricsDir      = flag.String("metrics-dir", "/data/metrics", "Directory to save metrics PNG files")
	metricsInterval = flag.Int("metrics-interval", 5, "Metrics collection interval in seconds")
	kubeconfig      = flag.String("kubeconfig", "", "Path to kubeconfig file (required)")
)

type Capture struct {
	ID      int
	Cmd     *exec.Cmd
	Started time.Time
}

type MetricPoint struct {
	Timestamp       time.Time
	ActiveSockets   int
	CPUUsage        float64
	CPUSoftIRQ      float64 // System interrupt (softirq) time - CRITICAL metric
	CPUUser         float64
	CPUSystem       float64
	CPUIdle         float64
	MemoryUsage     float64
	NetworkRx       float64 // MB/s
	NetworkTx       float64 // MB/s
	NetworkRxPPS    float64 // Packets per second
	NetworkTxPPS    float64 // Packets per second
	NetworkRxDrops  float64 // RX drops per second
	NetworkTxDrops  float64 // TX drops per second
	NetworkRxErrors float64 // RX errors per second
	NetworkTxErrors float64 // TX errors per second
	SoftIRQNETRX    float64 // NET_RX softirq rate
	SoftIRQNETTX    float64 // NET_TX softirq rate
}

type MetricsCollector struct {
	points        []MetricPoint
	startTime     time.Time
	outputDir     string
	prometheusURL string
	httpClient    *http.Client
	nodeFilter    string
	portForward   *portforward.PortForwarder
	stopChan      chan struct{}
}

// PrometheusResponse represents Prometheus API response
type PrometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func NewMetricsCollector(outputDir, promURL, nodeFilter string) *MetricsCollector {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Printf("Warning: Failed to create metrics directory: %v", err)
	}
	
	// Create HTTP client with bearer token from cluster
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	now := time.Now()
	return &MetricsCollector{
		points:        make([]MetricPoint, 0),
		startTime:     now,
		outputDir:     outputDir,
		prometheusURL: promURL,
		httpClient:    client,
		nodeFilter:   nodeFilter,
		stopChan:     make(chan struct{}),
	}
}

// Close stops port forwarding and cleans up resources
func (mc *MetricsCollector) Close() {
	if mc.stopChan != nil {
		close(mc.stopChan)
	}
	if mc.portForward != nil {
		mc.portForward.Close()
	}
}

func (mc *MetricsCollector) Collect(captures []*Capture, config *rest.Config) {
	activeCount := countActiveProcesses(captures)
	
	// Collect all metrics from Prometheus
	cpuMetrics := mc.getCPUMetrics(config)
	memUsage := mc.getMemoryUsage(config)
	netStats := mc.getNetworkStats(config)
	softIRQStats := mc.getSoftIRQStats(config)

	point := MetricPoint{
		Timestamp:       time.Now(),
		ActiveSockets:   activeCount,
		CPUUsage:        cpuMetrics.Usage,
		CPUSoftIRQ:      cpuMetrics.SoftIRQ, // CRITICAL: System interrupt time
		CPUUser:         cpuMetrics.User,
		CPUSystem:       cpuMetrics.System,
		CPUIdle:         cpuMetrics.Idle,
		MemoryUsage:     memUsage,
		NetworkRx:       netStats.RxMB,
		NetworkTx:       netStats.TxMB,
		NetworkRxPPS:    netStats.RxPPS,
		NetworkTxPPS:    netStats.TxPPS,
		NetworkRxDrops:  netStats.RxDrops,
		NetworkTxDrops:  netStats.TxDrops,
		NetworkRxErrors: netStats.RxErrors,
		NetworkTxErrors: netStats.TxErrors,
		SoftIRQNETRX:    softIRQStats.NETRX,
		SoftIRQNETTX:    softIRQStats.NETTX,
	}

	mc.points = append(mc.points, point)

	if *verbose {
		fmt.Printf("  Metrics: sockets=%d, cpu=%.2f%% (si=%.2f%%, user=%.2f%%, sys=%.2f%%), mem=%.2f%%, rx=%.2f MB/s (%.0f pps), tx=%.2f MB/s (%.0f pps), drops(rx/tx)=%.0f/%.0f, errors(rx/tx)=%.0f/%.0f, softirq(net_rx/net_tx)=%.0f/%.0f\n",
			activeCount, cpuMetrics.Usage, cpuMetrics.SoftIRQ, cpuMetrics.User, cpuMetrics.System, memUsage,
			netStats.RxMB, netStats.RxPPS, netStats.TxMB, netStats.TxPPS,
			netStats.RxDrops, netStats.TxDrops, netStats.RxErrors, netStats.TxErrors,
			softIRQStats.NETRX, softIRQStats.NETTX)
	}
}

func (mc *MetricsCollector) SaveCharts() error {
	if len(mc.points) < 2 {
		return fmt.Errorf("not enough data points to generate charts")
	}

	fmt.Println("\nGenerating metrics charts...")

	// Chart 1: Active Sockets Over Time
	if err := mc.plotActiveSockets(); err != nil {
		log.Printf("Failed to plot active sockets: %v", err)
	}

	// Chart 2: CPU Usage Over Time
	if err := mc.plotCPUUsage(); err != nil {
		log.Printf("Failed to plot CPU usage: %v", err)
	}

	// Chart 3: Memory Usage Over Time
	if err := mc.plotMemoryUsage(); err != nil {
		log.Printf("Failed to plot memory usage: %v", err)
	}

	// Chart 4: Network Throughput Over Time
	if err := mc.plotNetworkThroughput(); err != nil {
		log.Printf("Failed to plot network throughput: %v", err)
	}

	// Chart 5: Combined Metrics Overview
	if err := mc.plotCombinedMetrics(); err != nil {
		log.Printf("Failed to plot combined metrics: %v", err)
	}

	// Chart 6: System Interrupt (SoftIRQ) Time - CRITICAL
	if err := mc.plotSoftIRQTime(); err != nil {
		log.Printf("Failed to plot softirq time: %v", err)
	}

	// Chart 7: CPU Breakdown
	if err := mc.plotCPUBreakdown(); err != nil {
		log.Printf("Failed to plot CPU breakdown: %v", err)
	}

	// Chart 8: Network Packet Drops and Errors
	if err := mc.plotNetworkDropsErrors(); err != nil {
		log.Printf("Failed to plot network drops/errors: %v", err)
	}

	// Chart 9: SoftIRQ Rates (NET_RX/NET_TX)
	if err := mc.plotSoftIRQRates(); err != nil {
		log.Printf("Failed to plot softirq rates: %v", err)
	}

	fmt.Printf("✓ Metrics charts saved to %s\n", mc.outputDir)
	return nil
}

func (mc *MetricsCollector) plotActiveSockets() error {
	p := plot.New()
	p.Title.Text = "Active AF_PACKET Sockets Over Time"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Active Sockets"
	p.Legend.Top = true

	pts := make(plotter.XYs, len(mc.points))
	for i, point := range mc.points {
		pts[i].X = point.Timestamp.Sub(mc.startTime).Seconds()
		pts[i].Y = float64(point.ActiveSockets)
	}

	line, err := plotter.NewLine(pts)
	if err != nil {
		return err
	}
	line.Color = plotutil.Color(0)
	line.Width = vg.Points(2)

	p.Add(line)
	p.Legend.Add("Active Sockets", line)

	filename := fmt.Sprintf("%s/active_sockets.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotCPUUsage() error {
	p := plot.New()
	p.Title.Text = "CPU Usage Over Time (Total)"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "CPU Usage (%)"
	p.Legend.Top = true

	pts := make(plotter.XYs, len(mc.points))
	for i, point := range mc.points {
		pts[i].X = point.Timestamp.Sub(mc.startTime).Seconds()
		pts[i].Y = point.CPUUsage
	}

	line, err := plotter.NewLine(pts)
	if err != nil {
		return err
	}
	line.Color = plotutil.Color(1)
	line.Width = vg.Points(2)

	p.Add(line)
	p.Legend.Add("Total CPU Usage", line)

	filename := fmt.Sprintf("%s/cpu_usage.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotSoftIRQTime() error {
	p := plot.New()
	p.Title.Text = "System Interrupt (SoftIRQ) Time - CRITICAL METRIC"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "SoftIRQ Time (%)"
	p.Legend.Top = true

	pts := make(plotter.XYs, len(mc.points))
	for i, point := range mc.points {
		pts[i].X = point.Timestamp.Sub(mc.startTime).Seconds()
		pts[i].Y = point.CPUSoftIRQ
	}

	line, err := plotter.NewLine(pts)
	if err != nil {
		return err
	}
	line.Color = plotutil.Color(0) // Red for critical
	line.Width = vg.Points(3)

	p.Add(line)
	p.Legend.Add("SoftIRQ Time (%)", line)

	// Add threshold lines
	threshold50, _ := plotter.NewLine(plotter.XYs{{0, 50}, {float64(mc.points[len(mc.points)-1].Timestamp.Sub(mc.startTime).Seconds()), 50}})
	threshold50.Color = plotutil.Color(1)
	threshold50.LineStyle.Width = vg.Points(1)
	threshold50.LineStyle.Dashes = []vg.Length{vg.Points(5), vg.Points(5)}
	p.Add(threshold50)
	p.Legend.Add("Critical (50%)", threshold50)

	filename := fmt.Sprintf("%s/cpu_softirq_time.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotCPUBreakdown() error {
	p := plot.New()
	p.Title.Text = "CPU Breakdown by Mode"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "CPU Time (%)"
	p.Legend.Top = true

	userPts := make(plotter.XYs, len(mc.points))
	systemPts := make(plotter.XYs, len(mc.points))
	softIRQPts := make(plotter.XYs, len(mc.points))
	idlePts := make(plotter.XYs, len(mc.points))

	for i, point := range mc.points {
		time := point.Timestamp.Sub(mc.startTime).Seconds()
		userPts[i].X = time
		userPts[i].Y = point.CPUUser
		systemPts[i].X = time
		systemPts[i].Y = point.CPUSystem
		softIRQPts[i].X = time
		softIRQPts[i].Y = point.CPUSoftIRQ
		idlePts[i].X = time
		idlePts[i].Y = point.CPUIdle
	}

	userLine, _ := plotter.NewLine(userPts)
	userLine.Color = plotutil.Color(0)
	userLine.Width = vg.Points(2)

	systemLine, _ := plotter.NewLine(systemPts)
	systemLine.Color = plotutil.Color(1)
	systemLine.Width = vg.Points(2)

	softIRQLine, _ := plotter.NewLine(softIRQPts)
	softIRQLine.Color = plotutil.Color(2)
	softIRQLine.Width = vg.Points(3) // Thicker for visibility

	idleLine, _ := plotter.NewLine(idlePts)
	idleLine.Color = plotutil.Color(3)
	idleLine.Width = vg.Points(2)

	p.Add(userLine, systemLine, softIRQLine, idleLine)
	p.Legend.Add("User", userLine)
	p.Legend.Add("System", systemLine)
	p.Legend.Add("SoftIRQ (CRITICAL)", softIRQLine)
	p.Legend.Add("Idle", idleLine)

	filename := fmt.Sprintf("%s/cpu_breakdown.png", mc.outputDir)
	return p.Save(14*vg.Inch, 8*vg.Inch, filename)
}

func (mc *MetricsCollector) plotNetworkDropsErrors() error {
	p := plot.New()
	p.Title.Text = "Network Packet Drops and Errors"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Rate (per second)"
	p.Legend.Top = true

	rxDropPts := make(plotter.XYs, len(mc.points))
	txDropPts := make(plotter.XYs, len(mc.points))
	rxErrPts := make(plotter.XYs, len(mc.points))
	txErrPts := make(plotter.XYs, len(mc.points))

	for i, point := range mc.points {
		time := point.Timestamp.Sub(mc.startTime).Seconds()
		rxDropPts[i].X = time
		rxDropPts[i].Y = point.NetworkRxDrops
		txDropPts[i].X = time
		txDropPts[i].Y = point.NetworkTxDrops
		rxErrPts[i].X = time
		rxErrPts[i].Y = point.NetworkRxErrors
		txErrPts[i].X = time
		txErrPts[i].Y = point.NetworkTxErrors
	}

	rxDropLine, _ := plotter.NewLine(rxDropPts)
	rxDropLine.Color = plotutil.Color(0)
	rxDropLine.Width = vg.Points(2)

	txDropLine, _ := plotter.NewLine(txDropPts)
	txDropLine.Color = plotutil.Color(1)
	txDropLine.Width = vg.Points(2)

	rxErrLine, _ := plotter.NewLine(rxErrPts)
	rxErrLine.Color = plotutil.Color(2)
	rxErrLine.Width = vg.Points(2)

	txErrLine, _ := plotter.NewLine(txErrPts)
	txErrLine.Color = plotutil.Color(3)
	txErrLine.Width = vg.Points(2)

	p.Add(rxDropLine, txDropLine, rxErrLine, txErrLine)
	p.Legend.Add("RX Drops", rxDropLine)
	p.Legend.Add("TX Drops", txDropLine)
	p.Legend.Add("RX Errors", rxErrLine)
	p.Legend.Add("TX Errors", txErrLine)

	filename := fmt.Sprintf("%s/network_drops_errors.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotSoftIRQRates() error {
	p := plot.New()
	p.Title.Text = "SoftIRQ Rates - NET_RX and NET_TX"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "SoftIRQ Rate (per second)"
	p.Legend.Top = true

	netRxPts := make(plotter.XYs, len(mc.points))
	netTxPts := make(plotter.XYs, len(mc.points))

	for i, point := range mc.points {
		time := point.Timestamp.Sub(mc.startTime).Seconds()
		netRxPts[i].X = time
		netRxPts[i].Y = point.SoftIRQNETRX
		netTxPts[i].X = time
		netTxPts[i].Y = point.SoftIRQNETTX
	}

	netRxLine, _ := plotter.NewLine(netRxPts)
	netRxLine.Color = plotutil.Color(0)
	netRxLine.Width = vg.Points(2)

	netTxLine, _ := plotter.NewLine(netTxPts)
	netTxLine.Color = plotutil.Color(1)
	netTxLine.Width = vg.Points(2)

	p.Add(netRxLine, netTxLine)
	p.Legend.Add("NET_RX SoftIRQ", netRxLine)
	p.Legend.Add("NET_TX SoftIRQ", netTxLine)

	filename := fmt.Sprintf("%s/softirq_rates.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotMemoryUsage() error {
	p := plot.New()
	p.Title.Text = "Memory Usage Over Time"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Memory Usage (%)"
	p.Legend.Top = true

	pts := make(plotter.XYs, len(mc.points))
	for i, point := range mc.points {
		pts[i].X = point.Timestamp.Sub(mc.startTime).Seconds()
		pts[i].Y = point.MemoryUsage
	}

	line, err := plotter.NewLine(pts)
	if err != nil {
		return err
	}
	line.Color = plotutil.Color(2)
	line.Width = vg.Points(2)

	p.Add(line)
	p.Legend.Add("Memory Usage", line)

	filename := fmt.Sprintf("%s/memory_usage.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotNetworkThroughput() error {
	p := plot.New()
	p.Title.Text = "Network Throughput Over Time"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Throughput (MB/s)"
	p.Legend.Top = true

	rxPts := make(plotter.XYs, len(mc.points))
	txPts := make(plotter.XYs, len(mc.points))
	for i, point := range mc.points {
		time := point.Timestamp.Sub(mc.startTime).Seconds()
		rxPts[i].X = time
		rxPts[i].Y = point.NetworkRx
		txPts[i].X = time
		txPts[i].Y = point.NetworkTx
	}

	rxLine, err := plotter.NewLine(rxPts)
	if err != nil {
		return err
	}
	rxLine.Color = plotutil.Color(3)
	rxLine.Width = vg.Points(2)

	txLine, err := plotter.NewLine(txPts)
	if err != nil {
		return err
	}
	txLine.Color = plotutil.Color(4)
	txLine.Width = vg.Points(2)

	p.Add(rxLine, txLine)
	p.Legend.Add("RX (MB/s)", rxLine)
	p.Legend.Add("TX (MB/s)", txLine)

	filename := fmt.Sprintf("%s/network_throughput.png", mc.outputDir)
	return p.Save(12*vg.Inch, 6*vg.Inch, filename)
}

func (mc *MetricsCollector) plotCombinedMetrics() error {
	p := plot.New()
	p.Title.Text = "AF_PACKET Socket Issue - Combined Metrics Overview"
	p.X.Label.Text = "Time (seconds)"
	p.Y.Label.Text = "Value"
	p.Legend.Top = true

	// Normalize metrics for combined view
	socketPts := make(plotter.XYs, len(mc.points))
	cpuPts := make(plotter.XYs, len(mc.points))
	softIRQPts := make(plotter.XYs, len(mc.points))
	memPts := make(plotter.XYs, len(mc.points))

	maxSockets := 0.0
	maxCPU := 0.0
	maxSoftIRQ := 0.0
	maxMem := 0.0

	for _, point := range mc.points {
		if float64(point.ActiveSockets) > maxSockets {
			maxSockets = float64(point.ActiveSockets)
		}
		if point.CPUUsage > maxCPU {
			maxCPU = point.CPUUsage
		}
		if point.CPUSoftIRQ > maxSoftIRQ {
			maxSoftIRQ = point.CPUSoftIRQ
		}
		if point.MemoryUsage > maxMem {
			maxMem = point.MemoryUsage
		}
	}

	// Normalize to 0-100 scale
	for i, point := range mc.points {
		time := point.Timestamp.Sub(mc.startTime).Seconds()
		socketPts[i].X = time
		socketPts[i].Y = (float64(point.ActiveSockets) / maxSockets) * 100
		cpuPts[i].X = time
		cpuPts[i].Y = point.CPUUsage
		softIRQPts[i].X = time
		softIRQPts[i].Y = point.CPUSoftIRQ
		memPts[i].X = time
		memPts[i].Y = point.MemoryUsage
	}

	socketLine, _ := plotter.NewLine(socketPts)
	socketLine.Color = plotutil.Color(0)
	socketLine.Width = vg.Points(2)

	cpuLine, _ := plotter.NewLine(cpuPts)
	cpuLine.Color = plotutil.Color(1)
	cpuLine.Width = vg.Points(2)

	softIRQLine, _ := plotter.NewLine(softIRQPts)
	softIRQLine.Color = plotutil.Color(2)
	softIRQLine.Width = vg.Points(3) // Thicker for critical metric

	memLine, _ := plotter.NewLine(memPts)
	memLine.Color = plotutil.Color(3)
	memLine.Width = vg.Points(2)

	p.Add(socketLine, cpuLine, softIRQLine, memLine)
	p.Legend.Add(fmt.Sprintf("Sockets (max=%d)", int(maxSockets)), socketLine)
	p.Legend.Add("CPU Usage (%)", cpuLine)
	p.Legend.Add("SoftIRQ Time (%) - CRITICAL", softIRQLine)
	p.Legend.Add("Memory Usage (%)", memLine)

	filename := fmt.Sprintf("%s/combined_metrics.png", mc.outputDir)
	return p.Save(14*vg.Inch, 8*vg.Inch, filename)
}

func main() {
	flag.Parse()

	if *kubeconfig == "" {
		log.Fatalf("Error: --kubeconfig flag is required\nExample: --kubeconfig=./kubeconfig-sno1")
	}

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║    AF_PACKET Socket Issue Reproducer                    ║")
	fmt.Println("║    Based on RHEL-83393 reproducer                       ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Discover Prometheus from cluster
	fmt.Println("Discovering Prometheus from cluster...")
	promURL, config, nodeFilter, err := discoverPrometheus(*kubeconfig)
	if err != nil {
		log.Fatalf("Failed to discover Prometheus: %v", err)
	}

	fmt.Printf("Configuration:\n")
	fmt.Printf("  Kubeconfig:     %s\n", *kubeconfig)
	fmt.Printf("  Sockets:        %d\n", *socketCount)
	fmt.Printf("  Interface:      %s\n", *interface_)
	fmt.Printf("  Directory:      %s\n", *captureDir)
	fmt.Printf("  Metrics Dir:    %s\n", *metricsDir)
	fmt.Printf("  Metrics Interval: %ds\n", *metricsInterval)
	fmt.Printf("  Duration:        %ds (0 = infinite)\n", *duration)
	fmt.Printf("  Prometheus URL: %s\n", promURL)
	fmt.Printf("  Node Filter:    %s\n", nodeFilter)
	fmt.Println()

	// Create directories
	if err := os.MkdirAll(*captureDir, 0755); err != nil {
		log.Fatalf("Failed to create capture directory: %v", err)
	}
	if err := os.MkdirAll(*metricsDir, 0755); err != nil {
		log.Fatalf("Failed to create metrics directory: %v", err)
	}

	// Initialize metrics collector with Prometheus
	metricsCollector := NewMetricsCollector(*metricsDir, promURL, nodeFilter)
	defer metricsCollector.Close()
	
	// Set up port forwarding
	fmt.Println("Setting up port forwarding to Prometheus...")
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Find Prometheus pod
	namespace := "openshift-monitoring"
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=prometheus",
	})
	if err != nil || len(pods.Items) == 0 {
		namespace = "monitoring"
		pods, err = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=prometheus",
		})
	}
	if err != nil || len(pods.Items) == 0 {
		log.Fatalf("Failed to find Prometheus pod: %v", err)
	}

	podName := pods.Items[0].Name
	pf, err := setupPortForward(config, namespace, podName, "9090", "9090", metricsCollector.stopChan)
	if err != nil {
		log.Fatalf("Failed to set up port forwarding: %v", err)
	}
	metricsCollector.portForward = pf
	fmt.Printf("✓ Port forwarding established to %s/%s\n", namespace, podName)
	
	// Test Prometheus connection
	fmt.Println("Testing Prometheus connection...")
	if err := metricsCollector.testPrometheusConnection(config); err != nil {
		log.Fatalf("Failed to connect to Prometheus: %v", err)
	}
	fmt.Printf("✓ Connected to Prometheus\n")
	fmt.Println()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	captures := make([]*Capture, 0, *socketCount)

	// Start creating tcpdump processes
	fmt.Printf("Creating %d af_packet sockets...\n", *socketCount)
	startTime := time.Now()

	for i := 1; i <= *socketCount; i++ {
		capture := startTcpdump(i)
		if capture != nil {
			captures = append(captures, capture)
			
			if *verbose {
				fmt.Printf("  [%d/%d] Created socket %d\n", i, *socketCount, i)
			} else if i%50 == 0 {
				fmt.Printf("  Progress: %d/%d sockets created\n", i, *socketCount)
			}
			
			// Collect metrics periodically during creation
			if i%10 == 0 {
				metricsCollector.Collect(captures, config)
			}
			
			// Small delay to prevent overwhelming the system
			if i%50 == 0 {
				time.Sleep(500 * time.Millisecond)
			}
		} else {
			fmt.Printf("  WARNING: Failed to create socket %d\n", i)
		}
	}

	creationTime := time.Since(startTime)
	fmt.Printf("\n✓ Successfully created %d af_packet sockets in %v\n", len(captures), creationTime)
	fmt.Println()
	fmt.Println("Socket Statistics:")
	fmt.Printf("  Active:    %d\n", len(captures))
	fmt.Printf("  Directory: %s\n", *captureDir)
	fmt.Println()
	
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("Monitoring active... Press Ctrl+C to stop and cleanup")
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	// Monitor and report statistics
	ticker := time.NewTicker(time.Duration(*metricsInterval) * time.Second)
	defer ticker.Stop()

	var durationChan <-chan time.Time
	if *duration > 0 {
		durationChan = time.After(time.Duration(*duration) * time.Second)
	}

	// Monitoring loop
	for {
		select {
		case <-sigChan:
			fmt.Println("\n\n⚠ Interrupt received. Cleaning up...")
			metricsCollector.Collect(captures, config) // Final metrics collection
			cleanupCaptures(captures)
			if err := metricsCollector.SaveCharts(); err != nil {
				log.Printf("Warning: Failed to save charts: %v", err)
			}
			return

		case <-ticker.C:
			activeCount := countActiveProcesses(captures)
			fmt.Printf("[%s] Active sockets: %d/%d\n", 
				time.Now().Format("15:04:05"), activeCount, len(captures))
			metricsCollector.Collect(captures, config)

		case <-durationChan:
			fmt.Printf("\n✓ Duration %ds completed. Cleaning up...\n", *duration)
			metricsCollector.Collect(captures, config) // Final metrics collection
			cleanupCaptures(captures)
			if err := metricsCollector.SaveCharts(); err != nil {
				log.Printf("Warning: Failed to save charts: %v", err)
			}
			return
		}
	}
}

func startTcpdump(id int) *Capture {
	outputFile := fmt.Sprintf("%s/capture_%05d.pcap", *captureDir, id)
	
	// tcpdump -qnni <interface> -w <file>
	cmd := exec.Command("tcpdump", "-qnni", *interface_, "-w", outputFile)
	
	// Redirect stderr to /dev/null to suppress tcpdump warnings
	cmd.Stderr = nil
	
	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start tcpdump %d: %v", id, err)
		return nil
	}

	return &Capture{
		ID:      id,
		Cmd:     cmd,
		Started: time.Now(),
	}
}

func cleanupCaptures(captures []*Capture) {
	fmt.Println("\nStopping all tcpdump processes...")
	
	stopped := 0
	for _, capture := range captures {
		if capture.Cmd != nil && capture.Cmd.Process != nil {
			if err := capture.Cmd.Process.Signal(syscall.SIGTERM); err == nil {
				stopped++
			}
		}
	}
	
	// Wait a bit for graceful shutdown
	time.Sleep(2 * time.Second)
	
	// Force kill any remaining processes
	for _, capture := range captures {
		if capture.Cmd != nil && capture.Cmd.Process != nil {
			capture.Cmd.Process.Kill()
		}
	}
	
	fmt.Printf("✓ Stopped %d capture processes\n", stopped)
	
	// Report final statistics
	fmt.Println("\nFinal Statistics:")
	if fileCount, size := getCaptureStats(); fileCount > 0 {
		fmt.Printf("  Capture files: %d\n", fileCount)
		fmt.Printf("  Total size:    %s\n", formatBytes(size))
	}
	fmt.Println("\nCleanup complete")
}

func countActiveProcesses(captures []*Capture) int {
	active := 0
	for _, capture := range captures {
		if capture.Cmd != nil && capture.Cmd.Process != nil {
			// Try to signal with 0 to check if process exists
			if err := capture.Cmd.Process.Signal(syscall.Signal(0)); err == nil {
				active++
			}
		}
	}
	return active
}

func getCaptureStats() (int, int64) {
	entries, err := os.ReadDir(*captureDir)
	if err != nil {
		return 0, 0
	}

	count := 0
	var totalSize int64

	for _, entry := range entries {
		if !entry.IsDir() {
			info, err := entry.Info()
			if err == nil {
				count++
				totalSize += info.Size()
			}
		}
	}

	return count, totalSize
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// CPUMetrics holds CPU metrics from Prometheus
type CPUMetrics struct {
	Usage   float64
	SoftIRQ float64
	User    float64
	System  float64
	Idle    float64
}

// queryPrometheus executes a PromQL query and returns the result
func (mc *MetricsCollector) queryPrometheus(query string, config *rest.Config) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build query URL
	apiURL := fmt.Sprintf("%s/api/v1/query", mc.prometheusURL)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return 0, err
	}

	// Add query parameters
	q := req.URL.Query()
	q.Add("query", query)
	req.URL.RawQuery = q.Encode()

	// Add authentication token from kubeconfig
	if config.BearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.BearerToken))
	} else if config.BearerTokenFile != "" {
		token, err := os.ReadFile(config.BearerTokenFile)
		if err == nil {
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", strings.TrimSpace(string(token))))
		}
	}

	// Execute request
	resp, err := mc.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("Prometheus API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var promResp PrometheusResponse
	if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
		return 0, fmt.Errorf("failed to decode Prometheus response: %w", err)
	}

	if promResp.Status != "success" {
		return 0, fmt.Errorf("Prometheus query failed: %s", promResp.Status)
	}

	if len(promResp.Data.Result) == 0 {
		return 0, nil // No data, return 0
	}

	// Extract value (Prometheus returns [timestamp, value] as strings)
	valueStr, ok := promResp.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("invalid value format in Prometheus response")
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse value: %w", err)
	}

	return value, nil
}

// discoverPrometheus discovers Prometheus service from the cluster
func discoverPrometheus(kubeconfigPath string) (string, *rest.Config, string, error) {
	// Load kubeconfig
	var config *rest.Config
	var err error

	if kubeconfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			// Fallback to default kubeconfig location
			home := os.Getenv("HOME")
			if home != "" {
				kubeconfigPath = filepath.Join(home, ".kube", "config")
				config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
			}
		}
	}

	if err != nil {
		return "", nil, "", fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Try to find Prometheus service in openshift-monitoring namespace
	namespace := "openshift-monitoring"
	
	// Use port-forwarding to localhost
	localPort := "9090"
	promURL := fmt.Sprintf("http://localhost:%s", localPort)

	// Get pods for port-forwarding
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=prometheus",
	})
	if err != nil || len(pods.Items) == 0 {
		// Try default namespace
		namespace = "monitoring"
		pods, err = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=prometheus",
		})
		if err != nil || len(pods.Items) == 0 {
			// Try alternative label selector
			pods, err = clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
				LabelSelector: "prometheus=prometheus-k8s",
			})
		}
	}

	if err != nil || len(pods.Items) == 0 {
		return "", nil, "", fmt.Errorf("failed to find Prometheus pod: %w", err)
	}

	// Get node names for filtering
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to list nodes: %w", err)
	}

	var nodeNames []string
	for _, node := range nodes.Items {
		nodeNames = append(nodeNames, node.Name)
	}

	// Build node filter regex (match any node name)
	nodeFilter := strings.Join(nodeNames, "|")
	if nodeFilter != "" {
		nodeFilter = fmt.Sprintf(".*(%s).*", nodeFilter)
	}

	return promURL, config, nodeFilter, nil
}

// setupPortForward sets up port forwarding to Prometheus pod
func setupPortForward(config *rest.Config, namespace, podName, localPort, remotePort string, stopChan chan struct{}) (*portforward.PortForwarder, error) {
	// Build the URL for port forwarding
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName)
	hostIP := strings.TrimPrefix(config.Host, "https://")
	hostIP = strings.TrimPrefix(hostIP, "http://")
	
	// Parse URL
	urlStr := fmt.Sprintf("%s%s", config.Host, path)
	urlObj, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Create SPDY transport
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SPDY transport: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", urlObj)

	// Create port forwarder
	ports := []string{fmt.Sprintf("%s:%s", localPort, remotePort)}
	pf, err := portforward.New(dialer, ports, stopChan, make(chan struct{}), os.Stdout, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed to create port forwarder: %w", err)
	}

	// Start port forwarding in a goroutine
	go func() {
		if err := pf.ForwardPorts(); err != nil {
			log.Printf("Port forwarding error: %v", err)
		}
	}()

	// Wait a bit for port forwarding to establish
	time.Sleep(2 * time.Second)

	return pf, nil
}

// testPrometheusConnection tests the connection to Prometheus
func (mc *MetricsCollector) testPrometheusConnection(config *rest.Config) error {
	_, err := mc.queryPrometheus("up", config)
	return err
}

// getCPUMetrics returns CPU metrics from Prometheus
func (mc *MetricsCollector) getCPUMetrics(config *rest.Config) CPUMetrics {
	// Build node filter for queries
	nodeFilter := ""
	if mc.nodeFilter != "" {
		nodeFilter = fmt.Sprintf(`{instance=~"%s"}`, mc.nodeFilter)
	} else {
		nodeFilter = ""
	}

	// Query window (5 minutes)
	queryWindow := "5m"

	var metrics CPUMetrics

	// Total CPU usage (100 - idle)
	idleQuery := fmt.Sprintf(`100 - (avg(rate(node_cpu_seconds_total{mode="idle"%s}[%s])) * 100)`, nodeFilter, queryWindow)
	if idle, err := mc.queryPrometheus(idleQuery, config); err == nil {
		metrics.Usage = idle
	}

	// SoftIRQ time (CRITICAL)
	softIRQQuery := fmt.Sprintf(`100 * avg(rate(node_cpu_seconds_total{mode="softirq"%s}[%s]))`, nodeFilter, queryWindow)
	if si, err := mc.queryPrometheus(softIRQQuery, config); err == nil {
		metrics.SoftIRQ = si
	}

	// User time
	userQuery := fmt.Sprintf(`100 * avg(rate(node_cpu_seconds_total{mode="user"%s}[%s]))`, nodeFilter, queryWindow)
	if user, err := mc.queryPrometheus(userQuery, config); err == nil {
		metrics.User = user
	}

	// System time
	systemQuery := fmt.Sprintf(`100 * avg(rate(node_cpu_seconds_total{mode="system"%s}[%s]))`, nodeFilter, queryWindow)
	if system, err := mc.queryPrometheus(systemQuery, config); err == nil {
		metrics.System = system
	}

	// Idle time
	idleQuery = fmt.Sprintf(`100 * avg(rate(node_cpu_seconds_total{mode="idle"%s}[%s]))`, nodeFilter, queryWindow)
	if idle, err := mc.queryPrometheus(idleQuery, config); err == nil {
		metrics.Idle = idle
	}

	return metrics
}

// getMemoryUsage returns memory usage percentage from Prometheus
func (mc *MetricsCollector) getMemoryUsage(config *rest.Config) float64 {
	nodeFilter := ""
	if mc.nodeFilter != "" {
		nodeFilter = fmt.Sprintf(`{instance=~"%s"}`, mc.nodeFilter)
	}

	var total, available float64

	// Total memory
	totalQuery := fmt.Sprintf(`avg(node_memory_MemTotal_bytes%s)`, nodeFilter)
	if val, err := mc.queryPrometheus(totalQuery, config); err == nil {
		total = val
	}

	// Available memory
	availQuery := fmt.Sprintf(`avg(node_memory_MemAvailable_bytes%s)`, nodeFilter)
	if val, err := mc.queryPrometheus(availQuery, config); err == nil {
		available = val
	}

	if total == 0 {
		return 0.0
	}

	used := total - available
	return 100.0 * used / total
}

// NetworkStats holds comprehensive network statistics
type NetworkStats struct {
	RxMB    float64
	TxMB    float64
	RxPPS   float64 // Packets per second
	TxPPS   float64
	RxDrops float64
	TxDrops float64
	RxErrors float64
	TxErrors float64
}

// getNetworkStats returns comprehensive network statistics from Prometheus
func (mc *MetricsCollector) getNetworkStats(config *rest.Config) NetworkStats {
	nodeFilter := ""
	if mc.nodeFilter != "" {
		nodeFilter = fmt.Sprintf(`{instance=~"%s",device!="lo"}`, mc.nodeFilter)
	} else {
		nodeFilter = `{device!="lo"}`
	}

	queryWindow := "5m"
	var stats NetworkStats

	// RX throughput (MB/s)
	rxQuery := fmt.Sprintf(`sum(rate(node_network_receive_bytes_total%s[%s])) / 1024 / 1024`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(rxQuery, config); err == nil {
		stats.RxMB = val
	}

	// TX throughput (MB/s)
	txQuery := fmt.Sprintf(`sum(rate(node_network_transmit_bytes_total%s[%s])) / 1024 / 1024`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(txQuery, config); err == nil {
		stats.TxMB = val
	}

	// RX packets per second
	rxPPSQuery := fmt.Sprintf(`sum(rate(node_network_receive_packets_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(rxPPSQuery, config); err == nil {
		stats.RxPPS = val
	}

	// TX packets per second
	txPPSQuery := fmt.Sprintf(`sum(rate(node_network_transmit_packets_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(txPPSQuery, config); err == nil {
		stats.TxPPS = val
	}

	// RX drops per second
	rxDropQuery := fmt.Sprintf(`sum(rate(node_network_receive_drop_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(rxDropQuery, config); err == nil {
		stats.RxDrops = val
	}

	// TX drops per second
	txDropQuery := fmt.Sprintf(`sum(rate(node_network_transmit_drop_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(txDropQuery, config); err == nil {
		stats.TxDrops = val
	}

	// RX errors per second
	rxErrQuery := fmt.Sprintf(`sum(rate(node_network_receive_errs_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(rxErrQuery, config); err == nil {
		stats.RxErrors = val
	}

	// TX errors per second
	txErrQuery := fmt.Sprintf(`sum(rate(node_network_transmit_errs_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(txErrQuery, config); err == nil {
		stats.TxErrors = val
	}

	return stats
}

// SoftIRQStats holds softirq statistics
type SoftIRQStats struct {
	NETRX float64 // NET_RX softirq rate
	NETTX float64 // NET_TX softirq rate
}

// getSoftIRQStats returns softirq statistics from Prometheus
func (mc *MetricsCollector) getSoftIRQStats(config *rest.Config) SoftIRQStats {
	nodeFilter := ""
	if mc.nodeFilter != "" {
		nodeFilter = fmt.Sprintf(`{softirq="NET_RX",instance=~"%s"}`, mc.nodeFilter)
	} else {
		nodeFilter = `{softirq="NET_RX"}`
	}

	queryWindow := "5m"
	var stats SoftIRQStats

	// NET_RX softirq rate
	netRxQuery := fmt.Sprintf(`sum(rate(node_softirqs_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(netRxQuery, config); err == nil {
		stats.NETRX = val
	}

	// NET_TX softirq rate
	if mc.nodeFilter != "" {
		nodeFilter = fmt.Sprintf(`{softirq="NET_TX",instance=~"%s"}`, mc.nodeFilter)
	} else {
		nodeFilter = `{softirq="NET_TX"}`
	}
	netTxQuery := fmt.Sprintf(`sum(rate(node_softirqs_total%s[%s]))`, nodeFilter, queryWindow)
	if val, err := mc.queryPrometheus(netTxQuery, config); err == nil {
		stats.NETTX = val
	}

	return stats
}
