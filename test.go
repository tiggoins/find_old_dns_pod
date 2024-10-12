package main

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/wait"
	"time"
	"k8s.io/utils/clock"
)

func main() {
	delayFn := wait.Backoff{
		Duration: time.Second * 1,
		Cap:      time.Second * 10,
		Steps:    2, // now a required argument
		Factor:   0.2,
		Jitter:   0.1,
	  }.DelayWithReset(clock.RealClock{}, time.Second)

	stopCh := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Second)
		close(stopCh)
	}()

	// backoffManager := wait.NewExponentialBackoffManager(1*time.Second, 10*time.Second, 2*time.Second, 2.0, 1.0, clock.RealClock{})

	wait.BackoffUntil(func() {
		fmt.Printf("%s: Doing work\n", time.Now().Format(time.RFC3339))
	}, delayFn, true, stopCh)

	fmt.Println("Done")
}