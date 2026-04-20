// vapidgen: genera un par de claves VAPID (curve P-256) listas para usar
// como VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY en las env vars del server.
//
// Uso:
//
//	go run ./cmd/vapidgen
//
// Ejecutar UNA sola vez por ambiente. Si regenerás las keys, todas las
// subscriptions existentes quedan inválidas — el browser tiene que volver
// a suscribirse con la nueva public key.
package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error generando keys:", err)
		os.Exit(1)
	}
	fmt.Println("VAPID_PUBLIC_KEY=" + pub)
	fmt.Println("VAPID_PRIVATE_KEY=" + priv)
	fmt.Println("VAPID_SUBJECT=mailto:luciano.rodriguez.dev@gmail.com")
	fmt.Fprintln(os.Stderr, "\nCopiá esas 3 vars a Coolify (secrets del proyecto).")
}
