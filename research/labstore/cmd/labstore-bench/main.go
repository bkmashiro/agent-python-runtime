package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

func main() {
	defaults := labstore.DefaultBenchmarkConfig()
	var root string
	longSteps := uint64(defaults.LongSteps)
	branchChildren := uint64(defaults.BranchChildren)
	swarmAgents := uint64(defaults.SwarmAgents)
	swarmSteps := uint64(defaults.SwarmSteps)
	lowReuseItems := uint64(defaults.LowReuseItems)
	payloadBytes := uint64(defaults.PayloadBytes)
	flag.StringVar(&root, "root", "", "new absolute directory in which measured fixture stores will be retained")
	flag.Uint64Var(&longSteps, "long-steps", longSteps, "long sequential step count")
	flag.Uint64Var(&branchChildren, "branch-children", branchChildren, "branch child count")
	flag.Uint64Var(&swarmAgents, "swarm-agents", swarmAgents, "swarm agent count")
	flag.Uint64Var(&swarmSteps, "swarm-steps", swarmSteps, "steps per swarm agent")
	flag.Uint64Var(&lowReuseItems, "low-reuse-items", lowReuseItems, "low-reuse item count")
	flag.Uint64Var(&payloadBytes, "payload-bytes", payloadBytes, "synthetic shared or unique body size")
	flag.Parse()
	if root == "" {
		fmt.Fprintln(os.Stderr, "labstore-bench: -root is required")
		os.Exit(2)
	}
	for _, value := range []uint64{longSteps, branchChildren, swarmAgents, swarmSteps, lowReuseItems, payloadBytes} {
		if value > uint64(^uint32(0)) {
			fmt.Fprintln(os.Stderr, "labstore-bench: numeric flag exceeds uint32")
			os.Exit(2)
		}
	}
	config := labstore.BenchmarkConfig{
		LongSteps: uint32(longSteps), BranchChildren: uint32(branchChildren),
		SwarmAgents: uint32(swarmAgents), SwarmSteps: uint32(swarmSteps),
		LowReuseItems: uint32(lowReuseItems), PayloadBytes: uint32(payloadBytes),
	}
	report, err := labstore.RunBenchmarks(root, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "labstore-bench: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "labstore-bench: encode report: %v\n", err)
		os.Exit(1)
	}
}
