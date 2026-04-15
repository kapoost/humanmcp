// poetgen generates POET_POOL (base64-encoded JSON) and POET_SECRET (hex)
// for humanMCP rotating session passwords.
// Run once: go run ./cmd/poetgen/
// Then set the secrets on Fly.io:
//
//	fly secrets set POET_POOL="<base64>" POET_SECRET="<hex>"
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

var poems = []string{
	// Wisława Szymborska
	"nic dwa razy się nie zdarza",
	"kamień jest istotą doskonałą",
	"ludzie ludziom zgotowali ten los",
	"tyle wiemy o sobie ile nas sprawdzono",
	"wolę kino",
	"wolę siebie lubiącą ludzi",
	"preferuję zwierzęta",
	"ale się nie znam na mechanice",
	// Adam Mickiewicz
	"litwo ojczyzno moja",
	"nad wodą wielką i czystą",
	"polały się łzy me czyste rzęsiste",
	"stąd nikt nie wjeżdża do wielkiego świata",
	"ciemno wszędzie głucho wszędzie",
	// Juliusz Słowacki
	"chodzi mi o to aby język giętki",
	"smutno mi boże",
	"anioł ognisty mój anioł lewy",
	// Cyprian Kamil Norwid
	"snuć miłość jak jedwabnik nić",
	"bo piękno na to jest by zachwycało",
	"odpowiednie dać rzeczy słowo",
	// Zbigniew Herbert
	"wolę wróble na dachu",
	"pan cogito myśli o powrocie",
	"kamyk jest stworzeniem doskonałym",
	"trzeba dać świadectwo",
	"idź dokąd poszli tamci",
	// Bolesław Leśmian
	"kto nie dotknął ziemi ni razu",
	"w malinowym chruśniaku",
	"dziewczyna szła i szła do sadu",
	// Maria Pawlikowska-Jasnorzewska
	"powiedz mi jak mnie kochasz",
	"kocham cię w każdej chwili",
	// Leopold Staff
	"deszcz jesienny pada i pada",
	"być ptakiem co śpiewa",
	// Julian Tuwim
	"mieszkańcy panowie lokatorzy",
	"kwiaty polskie",
	"sitowie",
	"słoń trąbalski",
	// Tadeusz Różewicz
	"ocalony",
	"to są nazwy puste i jednoznaczne",
	// Czesław Miłosz
	"który skrzywdziłeś człowieka prostego",
	"w mojej ojczyźnie do której nie wrócę",
	"dar",
	// Krzysztof Kamil Baczyński
	"biała magia",
	"pokolenie",
	"elegia o chłopcu polskim",
	// Kazimierz Przerwa-Tetmajer
	"lubię kiedy kobieta",
	"melodia mgieł nocnych",
	// Jan Kochanowski
	"czego chcesz od nas panie",
	"nie porzucaj nadzieje",
	// Konstanty Ildefons Gałczyński
	"proszę państwa do gazu",
	"zaczarowana dorożka",
	// Hymns / Patriotic
	"jeszcze polska nie zginęła",
	"rota nie rzucim ziemi",
}

func main() {
	// Generate random 32-byte secret
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		fmt.Fprintf(os.Stderr, "error generating secret: %v\n", err)
		os.Exit(1)
	}
	secretHex := hex.EncodeToString(secret)

	// Encode pool as base64 JSON
	poolJSON, err := json.Marshal(poems)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error encoding pool: %v\n", err)
		os.Exit(1)
	}
	poolB64 := base64.StdEncoding.EncodeToString(poolJSON)

	fmt.Println("=== humanMCP Rotating Poet Passwords ===")
	fmt.Println()
	fmt.Printf("Pool: %d poems\n", len(poems))
	fmt.Printf("Secret: %s\n", secretHex)
	fmt.Println()
	fmt.Println("Run this in your terminal to set Fly secrets:")
	fmt.Println()
	fmt.Printf("fly secrets set \\\n")
	fmt.Printf("  POET_POOL=\"%s\" \\\n", poolB64)
	fmt.Printf("  POET_SECRET=\"%s\"\n", secretHex)
	fmt.Println()
	fmt.Println("KEEP POET_SECRET private. The pool is not secret but")
	fmt.Println("without the secret nobody can predict which poem is active.")
}
