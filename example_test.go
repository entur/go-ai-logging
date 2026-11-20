package ailogging_test

import (
	"context"
	"fmt"

	ailogging "github.com/entur/go-ai-logging"
)

func ExampleParseArgs() {
	cfg, err := ailogging.ParseArgs([]string{"-ailog.out=_artifacts", "-run", "TestPayment", "./...", "-args", "-ailog.out=child"})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(cfg.OutParent)
	fmt.Println(cfg.Args)
	// Output:
	// _artifacts
	// [-run TestPayment ./... -args -ailog.out=child]
}

func ExampleRun() {
	res := ailogging.Run(context.Background(), ailogging.Config{
		Args:      []string{"-run", "TestPayment", "./payment"},
		OutParent: "_artifacts",
	})
	if res.Err != nil {
		fmt.Println("capture incomplete:", res.Err)
	}
	fmt.Println("run files in", res.RunDir, "go test exited with", res.GoExit)
}
