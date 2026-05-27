package main
import "fmt"
func main() {
    size := 200 * 1024 * 1024
    _ = make([]byte, size)
    fmt.Println("allocated 200MB")
}
