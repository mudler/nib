package promise_test

import (
	"fmt"

	"github.com/mudler/nib/promise"
)

func ExamplePromises() {
	fmt.Println(promise.Promises("The build is running. I'll check on it shortly."))
	fmt.Println(promise.Promises("Controllo tra poco."))
	fmt.Println(promise.Promises("Do you want me to check later?"))
	// Output:
	// true
	// true
	// false
}

func ExampleFind() {
	m, ok := promise.Find("Done. I'll wait and move it when it finishes.")
	fmt.Println(ok, m.Language, m.Sentence)
	// Output: true en I'll wait and move it when it finishes
}
