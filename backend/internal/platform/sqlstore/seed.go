package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func SeedDatabase(ctx context.Context, store *Store) error {
	departments := []struct{ id, name, short string }{
		{"dept-pra-konstruksi", "Pra Konstruksi", "PRA"},
		{"dept-operasi-1", "Operasi Konstruksi 1", "OPK 1"},
		{"dept-operasi-2", "Operasi Konstruksi 2", "OPK 2"},
		{"dept-keuangan", "Keuangan, Umum & SDM", "KUSD"},
		{"dept-csr-umum", "CSR & Umum", "CSR"},
	}
	for _, d := range departments {
		_, err := store.exec(ctx, `INSERT INTO departments(id,name,short_name,color) VALUES(?,?,?,'#2563EB') ON CONFLICT(name) DO NOTHING`, d.id, d.name, d.short)
		if err != nil {
			return err
		}
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	rooms := []struct {
		id, name, kind, platform, location string
		capacity, breakout                 int
		facilities                         []string
	}{
		{"room-uip-1", "R. Rapat Lt. 1", "PHYSICAL", "", "Gedung UIP", 20, 0, []string{"LCD", "Mic", "WiFi"}},
		{"room-uip-2", "R. Rapat Lt. 2", "PHYSICAL", "", "Gedung UIP", 16, 0, []string{"LCD", "Mic", "WiFi"}},
		{"room-uip-3", "R. Rapat Lt. 3", "PHYSICAL", "", "Gedung UIP", 25, 0, []string{"LCD", "Mic", "WiFi"}},
		{"room-uip-4", "R. Serbaguna Lt. 3", "PHYSICAL", "", "Gedung UIP", 50, 0, []string{"LCD", "Mic", "WiFi", "Stage"}},
		{"room-uip-zoom", "Zoom Meeting Utama", "ONLINE", "ZOOM", "Online", 300, 10, []string{"Video Conference"}},
		{"room-uip-teams", "Microsoft Teams Meeting", "ONLINE", "TEAMS", "Online", 300, 50, []string{"Video Conference", "Teams breakout rooms"}},
	}
	for _, room := range rooms {
		features, _ := json.Marshal(room.facilities)
		_, err := store.exec(ctx, `INSERT INTO rooms(id,name,kind,online_platform,capacity,location,facilities,breakout_rooms,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,1,?,?) ON CONFLICT(id) DO NOTHING`, room.id, room.name, room.kind, nullable(room.platform), room.capacity, room.location, string(features), room.breakout, stamp, stamp)
		if err != nil {
			return err
		}
		if room.platform == "ZOOM" && room.breakout > 0 {
			slotCapacity := (room.capacity + room.breakout - 1) / room.breakout
			for i := 1; i <= room.breakout; i++ {
				_, err = store.exec(ctx, `INSERT INTO zoom_breakout_rooms(id,resource_id,name,capacity,active) VALUES(?,?,?,?,1) ON CONFLICT(resource_id,name) DO UPDATE SET capacity=excluded.capacity,active=1`, fmt.Sprintf("%s-slot-%d", room.id, i), room.id, fmt.Sprintf("Breakout %d", i), slotCapacity)
				if err != nil {
					return err
				}
			}
		}
	}
	password := os.Getenv("DEMO_PASSWORD")
	if password == "" {
		return fmt.Errorf("DEMO_PASSWORD must be set explicitly before seeding demo accounts")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	users := []struct{ id, name, email, role, dept string }{
		{"demo-admin", "Administrator UIP", "admin@uip.local", "ADMIN", "dept-pra-konstruksi"},
		{"demo-approver", "Approver UIP", "approver@uip.local", "APPROVER", "dept-csr-umum"},
	}
	for _, u := range users {
		_, err := store.exec(ctx, `INSERT INTO users(id,name,email,password_hash,role,department_id,page_access,active,created_at,updated_at) VALUES(?,?,?,?,?,?, '[]',1,?,?) ON CONFLICT(email) DO NOTHING`, u.id, u.name, u.email, string(hash), u.role, u.dept, stamp, stamp)
		if err != nil {
			return err
		}
	}
	_, err = store.exec(ctx, `INSERT INTO settings(key,value,updated_at) VALUES('allowSelfApproval','false',?) ON CONFLICT(key) DO NOTHING`, stamp)
	if err != nil {
		return err
	}
	_, err = store.exec(ctx, `INSERT INTO announcements(id,type,title,body,published_at) VALUES('announcement-welcome','GENERAL','UIP Meeting Portal','Gunakan portal ini untuk mengajukan dan mengelola rapat.',?) ON CONFLICT(id) DO NOTHING`, stamp)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(os.Stdout, "Seeded admin@uip.local and approver@uip.local; set DEMO_PASSWORD to override the local demo password."); err != nil {
		return err
	}
	return nil
}
