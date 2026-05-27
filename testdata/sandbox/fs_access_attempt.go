package main
import ("fmt"; "os")
func main() {
    _, err := os.ReadFile("/etc/passwd")
    if err != nil { fmt.Println("FS access correctly blocked:", err) } else { fmt.Println("SECURITY: filesystem access should be blocked"); os.Exit(1) }
}
