# dualnet

Combine plusieurs connexions internet sur un PC Windows (ex. Wi‑Fi de la box + partage USB du téléphone), **sans serveur distant**.

## Modes

- **`get`** : télécharge un fichier par morceaux (HTTP Range) répartis sur tous les liens. Les débits **s'additionnent** pour ce fichier.
- **`proxy`** : proxy local SOCKS5 + HTTP qui répartit les **connexions** entre les liens (navigation, torrents, téléchargements multiples). Une connexion donnée reste sur un seul lien.

## Interface

```powershell
.\dualnet.exe ui
```

Ouvre un tableau de bord dans le navigateur (`http://127.0.0.1:8090`) : débit de chaque lien en direct (courbes sur 2 min), lancement de téléchargements combinés (dans `Téléchargements`), et proxy sur `127.0.0.1:1080`.

## Utilisation en ligne de commande

```powershell
go build -o dualnet.exe .
.\dualnet.exe interfaces
.\dualnet.exe get https://exemple.com/gros-fichier.iso
.\dualnet.exe get -if "Wi-Fi,Ethernet 2" -o fichier.iso https://...
.\dualnet.exe proxy -listen 127.0.0.1:1080
```

Pour le proxy, configurez l'application (navigateur, qBittorrent…) en SOCKS5 `127.0.0.1:1080` ou HTTP `127.0.0.1:1080`.

## Le Wi‑Fi se coupe quand le téléphone est branché ?

Windows limite par défaut les connexions simultanées : le partage USB est vu comme une connexion filaire, et le Wi‑Fi est déconnecté. Pour l'autoriser (PowerShell administrateur) :

```powershell
New-Item -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy' -Force | Out-Null
Set-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy' -Name fMinimizeConnections -Value 0 -Type DWord
```

dualnet (modes `ui` et `proxy`) vérifie les interfaces toutes les 2 s : un lien qui revient est réutilisé sans redémarrer.

## Limites

Sans serveur intermédiaire, un flux unique (vidéo, jeu, visio) ne peut pas utiliser deux liens à la fois.
