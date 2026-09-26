package main

import "fmt"

func route(path string) string {
    return fmt.Sprintf("routing to %s", path)
}

func main() {
    fmt.Println(route("/home"))
}
