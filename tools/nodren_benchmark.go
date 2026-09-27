//go:build !benchmark_v1 && !benchmark_ipc && !benchmark_batching

package main

func main() {
	runV1Bench()
}
