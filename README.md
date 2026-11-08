# dualnet

Combine plusieurs connexions internet sur un PC Windows (ex. Wi‑Fi de la box + partage USB du téléphone), **sans serveur distant**.

## Modes

- **`get`** : télécharge un fichier par morceaux (HTTP Range) répartis sur tous les liens. Les débits **s'additionnent** pour ce fichier.
- **`proxy`** : proxy local SOCKS5 + HTTP qui répartit les **connexions** entre les liens (navigation, torrents, téléchargements multiples). Une connexion donnée reste sur un seul lien.

## Utilisation

```powershell
go build -o dualnet.exe .
.\dualnet.exe interfaces
.\dualnet.exe get https://exemple.com/gros-fichier.iso
.\dualnet.exe get -if "Wi-Fi,Ethernet 2" -o fichier.iso https://...
.\dualnet.exe proxy -listen 127.0.0.1:1080
```

Pour le proxy, configurez l'application (navigateur, qBittorrent…) en SOCKS5 `127.0.0.1:1080` ou HTTP `127.0.0.1:1080`.

## Limites

Sans serveur intermédiaire, un flux unique (vidéo, jeu, visio) ne peut pas utiliser deux liens à la fois.
