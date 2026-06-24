package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/msTimofeev/rospanel/internal/backup"
	"github.com/msTimofeev/rospanel/internal/model"
	"github.com/msTimofeev/rospanel/internal/store"
	"github.com/msTimofeev/rospanel/internal/updater"
	"github.com/msTimofeev/rospanel/internal/version"
)

// printUsage writes the CLI help to w.
func printUsage(w io.Writer) {
	fmt.Fprint(w, `rospanel `+version.Version+` — панель управления VPN (Xray + sing-box)

Использование:
  rospanel [команда] [аргументы]

Без команды запускается сервер панели (обычно его запускает systemd).

Команды:
  install            Установить и запустить systemd-сервис (root, только Linux).
  uninstall [-y]     Удалить systemd-сервис (данные в каталоге данных сохраняются).
  start              Запустить сервис (systemctl start).
  stop               Остановить сервис (systemctl stop).
  restart            Перезапустить сервис (systemctl restart).
  status             Показать статус сервиса (systemctl status).
  update [-y]        Обновиться до последнего релиза с GitHub и перезапуститься.
  backup [файл]      Создать бэкап .tar.gz (БД + сертификаты + конфиг Xray).
                     Без аргумента имя файла — с текущей датой/временем.
  restore [-y] <файл>
                     Подготовить восстановление из бэкапа; применится при
                     следующем старте панели (нужно перезапустить сервис).
  host [-y] [домен|IP]
                     Показать текущий адрес или сменить домен/IP (перевыпуск TLS).
  reset [-y]         Сброс к заводским настройкам — стирает всю базу данных.
  version            Показать версию.
  help               Показать эту справку.

Флаги:
  -y, --yes          Не запрашивать подтверждение
                     (для update, reset, uninstall, restore, host).

Примеры:
  sudo rospanel install
  rospanel backup /root/rospanel.tar.gz
  rospanel restore /root/rospanel.tar.gz && systemctl restart rospanel
  rospanel host vpn.example.com
  rospanel update -y
`)
}

func runBackup(dataDir string, args []string) {
	out := fmt.Sprintf("rospanel-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	if len(args) > 0 {
		out = args[0]
	}
	if err := backup.Create(dataDir, out); err != nil {
		log.Fatalf("backup failed: %v", err)
	}
	log.Printf("backup written: %s", out)
}

func runRestore(dataDir string, args []string) {
	src := firstPositional(args)
	if src == "" {
		log.Fatal("usage: rospanel restore [-y] <backup.tar.gz>")
	}
	if !hasYesFlag(args) && !confirmTTY(
		"Восстановление из бэкапа ЗАМЕНИТ все текущие данные при следующем старте панели:\n"+
			"пользователи, настройки, домен/TLS, секретный путь. Текущие данные будут\n"+
			"потеряны — сделайте бэкап заранее.\n"+
			"Продолжить? [y/N]: ") {
		fmt.Println("Отменено.")
		return
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		log.Fatalf("restore: %v", err)
	}
	if err := backup.StageRestore(src, dataDir); err != nil {
		log.Fatalf("restore failed: %v", err)
	}
	log.Printf("restore staged from %s — (re)start the panel to apply it", src)
}

// firstPositional returns the first non-flag argument (flags start with "-").
func firstPositional(args []string) string {
	for _, a := range args {
		if a != "" && !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// runHost prints or sets the panel host (domain or IP). Setting it drops the
// current certificate so a fresh one is issued for the new host on restart, then
// restarts the service (when managed by systemd) to apply it.
func runHost(dataDir string, args []string) {
	st, err := store.Open(filepath.Join(dataDir, "rospanel.db"))
	if err != nil {
		log.Fatalf("host: open store: %v", err)
	}
	defer st.Close()
	cur, err := st.GetSettings()
	if err != nil {
		log.Fatalf("host: %v", err)
	}
	host := strings.TrimSpace(firstPositional(args))
	if host == "" {
		// No target given → just report the current host.
		if cur.Host == "" {
			log.Print("no host configured yet")
		} else {
			log.Printf("current host: %s", cur.Host)
		}
		return
	}
	if !hasYesFlag(args) && !confirmTTY(fmt.Sprintf(
		"Сменить адрес панели на %q?\n"+
			"  Будет перевыпущен TLS-сертификат (нужен открытый порт 80), а клиентам и\n"+
			"  подпискам понадобится новый адрес. Сервис перезапустится.\n"+
			"Продолжить? [y/N]: ", host)) {
		fmt.Println("Отменено.")
		return
	}
	certPath := filepath.Join(dataDir, "certs", "cert.pem")
	keyPath := filepath.Join(dataDir, "certs", "key.pem")
	if err := st.SetTLS(host, host, model.TLSModeACME, certPath, keyPath); err != nil {
		log.Fatalf("host: %v", err)
	}
	// Drop the existing cert so the next boot issues a fresh one for the new host
	// (otherwise the still-valid old cert would be kept).
	_ = os.Remove(certPath)
	_ = os.Remove(keyPath)
	log.Printf("host set to %q", host)

	if _, err := exec.LookPath("systemctl"); err == nil {
		log.Print("restarting rospanel to issue the certificate…")
		c := exec.Command("systemctl", "restart", "rospanel")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			log.Printf("auto-restart failed: %v — restart the panel manually", err)
		}
	} else {
		log.Print("restart the panel to issue the certificate")
	}
}

const (
	systemdUnitPath = "/etc/systemd/system/rospanel.service"
	installBinPath  = "/usr/local/bin/rospanel"
)

// runInstall installs rospanel as a systemd service: copy the running binary to
// /usr/local/bin, write a unit (carrying through any ROSPANEL_HOST /
// ROSPANEL_ACME_EMAIL / XRAY_BIN set in the current environment), then enable and
// (re)start it. Idempotent — re-run to update the unit or the binary.
func runInstall() {
	if runtime.GOOS != "linux" {
		log.Fatal("install: systemd setup is Linux-only")
	}
	if os.Geteuid() != 0 {
		log.Fatal("install: run as root (sudo)")
	}
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("install: locate binary: %v", err)
	}
	if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
		self = resolved
	}
	if self != installBinPath {
		if err := copyFile(self, installBinPath, 0o755); err != nil {
			log.Fatalf("install: copy binary to %s: %v", installBinPath, err)
		}
		log.Printf("install: copied binary → %s", installBinPath)
	}

	dataDir := env("ROSPANEL_DATA", "/var/lib/rospanel")
	envLines := []string{
		"Environment=ROSPANEL_DATA=" + dataDir,
		// Internal loopback for the VLESS default fallback. Kept off 8080 so that
		// port is free for the optional VLESS-REALITY inbound.
		"Environment=ROSPANEL_ADMIN_ADDR=127.0.0.1:8080",
	}
	// Carry through optional config the operator passed when running install.
	for _, k := range []string{"ROSPANEL_HOST", "ROSPANEL_ACME_EMAIL", "XRAY_BIN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			envLines = append(envLines, "Environment="+k+"="+v)
		}
	}

	unit := "[Unit]\n" +
		"Description=RosPanel VPN Panel\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"Type=simple\n" +
		strings.Join(envLines, "\n") + "\n" +
		"ExecStart=" + installBinPath + "\n" +
		"Restart=always\n" +
		"RestartSec=3\n" +
		"AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN\n" +
		"StateDirectory=rospanel\n\n" +
		"[Install]\n" +
		"WantedBy=multi-user.target\n"
	if err := os.WriteFile(systemdUnitPath, []byte(unit), 0o644); err != nil {
		log.Fatalf("install: write unit: %v", err)
	}
	log.Printf("install: wrote %s", systemdUnitPath)

	for _, args := range [][]string{{"daemon-reload"}, {"enable", "rospanel"}, {"restart", "rospanel"}} {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("install: systemctl %s: %v", strings.Join(args, " "), err)
		}
	}
	log.Print("install: done — service enabled and started")
	log.Print("first-run credentials: journalctl -u rospanel | grep -A6 FIRST-RUN")
}

// runUninstall stops/disables the service and removes the unit file. Data under
// ROSPANEL_DATA is left untouched.
func runUninstall(args []string) {
	if os.Geteuid() != 0 {
		log.Fatal("uninstall: run as root (sudo)")
	}
	if !hasYesFlag(args) && !confirmTTY(
		"Удалить systemd-сервис rospanel? Панель будет остановлена.\n"+
			"Данные в каталоге данных (/var/lib/rospanel) сохранятся, бинарь не удаляется.\n"+
			"Продолжить? [y/N]: ") {
		fmt.Println("Отменено.")
		return
	}
	_ = exec.Command("systemctl", "disable", "--now", "rospanel").Run()
	if err := os.Remove(systemdUnitPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("uninstall: remove unit: %v", err)
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	log.Printf("uninstall: removed %s (data left in place)", systemdUnitPath)
}

// runService controls the systemd unit: start / stop / restart / status.
func runService(action string) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		log.Fatalf("%s: systemctl не найден (управление сервисом доступно только на Linux + systemd)", action)
	}
	c := exec.Command("systemctl", action, "rospanel")
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	err := c.Run()
	if action == "status" {
		return // `status` exits non-zero when inactive; its output is already shown
	}
	if err != nil {
		log.Fatalf("%s: не удалось (%v) — нужен root, попробуйте sudo", action, err)
	}
	log.Printf("%s: готово", action)
}

// copyFile copies src to dst atomically (write to .new, then rename) with mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// runUpdate is the `rospanel update [-y]` CLI: check the baked-in GitHub repo for
// a newer release, ask for confirmation, then download + verify + atomically swap
// the binary (snapshotting the DB first) and restart the service. `-y` skips the
// prompt for non-interactive use.
func runUpdate(args []string) {
	yes := hasYesFlag(args)
	ctx := context.Background()
	repo := updater.Repo
	if r := strings.TrimSpace(os.Getenv("ROSPANEL_REPO")); r != "" {
		repo = r
	}

	fmt.Printf("Текущая версия: v%s\n", version.Version)
	rel, err := updater.Latest(ctx, repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
	if !updater.IsNewer(rel.Version, version.Version) {
		fmt.Println("У вас последняя версия.")
		return
	}
	if rel.AssetURL == "" {
		fmt.Fprintf(os.Stderr, "В релизе v%s нет файла %s.\n", rel.Version, updater.AssetName)
		os.Exit(1)
	}

	fmt.Printf("Доступна версия v%s.\n", rel.Version)
	if !yes && !confirmTTY(fmt.Sprintf(
		"Обновить v%s → v%s? Сервис перезапустится, подключения кратко прервутся. [y/N]: ",
		version.Version, rel.Version)) {
		fmt.Println("Отменено.")
		return
	}

	fmt.Println("Скачиваю и устанавливаю…")
	dataDir := resolveDataDir()
	backupFn := func() error {
		return backup.Create(dataDir, filepath.Join(dataDir, "pre-update-backup.tgz"))
	}
	if err := updater.Apply(ctx, rel, backupFn); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}

	fmt.Println("Перезапускаю сервис…")
	if err := exec.Command("systemctl", "restart", "rospanel").Run(); err != nil {
		fmt.Fprintf(os.Stderr,
			"Бинарь обновлён, но перезапуск не удался: %v\nЗапустите вручную: systemctl restart rospanel\n", err)
		os.Exit(1)
	}
	fmt.Printf("Готово — обновлено до v%s.\n", rel.Version)
}

// runReset is the `rospanel reset [-y]` CLI: a FULL factory reset. It stops the
// service, deletes the database (ALL settings, users AND admins), then starts it
// again — which re-bootstraps a clean install (fresh admin credentials printed to
// the log, host/domain/secret regenerated, first-run wizard). TLS cert files on
// disk are left in place (reused once a domain is configured again).
func runReset(args []string) {
	if !hasYesFlag(args) && !confirmTTY(
		"ПОЛНЫЙ СБРОС панели к заводским настройкам.\n"+
			"Будет СТЁРТО ВСЁ: пользователи, админ-аккаунт, домен/TLS, секретный путь,\n"+
			"протоколы, порты, роутинг, прокси, DNS — вся база данных.\n"+
			"После сброса: вход admin/admin, путь /rospanel/, повторная первичная настройка.\n"+
			"Действие необратимо. Продолжить? [y/N]: ") {
		fmt.Println("Отменено.")
		return
	}

	dataDir := resolveDataDir()
	// Stop the service so the DB files aren't held open (and re-created) while we
	// delete them. Refuse to delete the DB out from under a still-live process.
	hasSystemctl := false
	if _, err := exec.LookPath("systemctl"); err == nil {
		hasSystemctl = true
		_ = exec.Command("systemctl", "stop", "rospanel").Run()
		if out, _ := exec.Command("systemctl", "is-active", "rospanel").Output(); strings.TrimSpace(string(out)) == "active" {
			log.Fatal("reset: сервис rospanel всё ещё активен — остановите его вручную (systemctl stop rospanel) и повторите")
		}
	} else {
		log.Print("reset: systemctl не найден — остановите запущенную панель вручную перед сбросом, иначе удаление БД не применится")
	}

	removed := false
	for _, f := range []string{"rospanel.db", "rospanel.db-wal", "rospanel.db-shm"} {
		err := os.Remove(filepath.Join(dataDir, f))
		switch {
		case err == nil:
			removed = true
		case os.IsNotExist(err):
			// nothing to remove
		default:
			fmt.Fprintf(os.Stderr, "Не удалось удалить %s: %v\n", f, err)
			os.Exit(1)
		}
	}
	if !removed {
		fmt.Fprintln(os.Stderr, "База данных не найдена — нечего сбрасывать.")
	}

	if hasSystemctl {
		if err := exec.Command("systemctl", "start", "rospanel").Run(); err != nil {
			fmt.Fprintf(os.Stderr,
				"База удалена, но сервис не запустился: %v\nЗапустите вручную: systemctl start rospanel\n", err)
			os.Exit(1)
		}
	}
	fmt.Println("Готово — панель сброшена к заводским настройкам.")
	fmt.Println("Вход: admin / admin · путь панели: /rospanel/ (первичная настройка попросит сменить пароль).")
}

// hasYesFlag reports whether the CLI args carry a non-interactive confirm flag.
func hasYesFlag(args []string) bool {
	for _, a := range args {
		if a == "-y" || a == "--yes" || a == "--force" {
			return true
		}
	}
	return false
}

// confirmTTY prompts on stdin and returns true only for an explicit yes.
func confirmTTY(prompt string) bool {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "д", "да":
		return true
	}
	return false
}
