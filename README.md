# dualnet

Combine plusieurs connexions internet sur un PC Windows (ex. Wi‑Fi de la box + partage USB du téléphone), **sans serveur distant**.

## Démarrage rapide

Double-cliquez sur `dualnet.exe` :

1. Windows demande les droits administrateur (nécessaires au mode transparent) ;
2. une icône apparaît dans la barre des tâches et l'interface s'ouvre dans le navigateur (`http://127.0.0.1:8090`) ;
3. le **mode transparent** est actif : tout le trafic IPv4 du PC (Steam, jeux, navigateur…) est réparti sur les liens.

Menu de l'icône : ouvrir l'interface, activer/désactiver le mode transparent, **démarrer avec Windows**, quitter (le réseau redevient normal).

## Ce que fait dualnet

| Mode | Pour quoi | Gain |
|---|---|---|
| **Transparent** (carte virtuelle Wintun) | Tout le PC, sans réglage | Les connexions sont réparties entre les liens |
| **Proxy** SOCKS5/HTTP `127.0.0.1:1080` | Applis réglées sur le proxy | Idem, appli par appli |
| **Téléchargement** (`get` ou interface) | Un gros fichier | Les débits **s'additionnent** sur ce fichier |

- **Répartition selon le débit réel** : chaque nouvelle connexion va au lien qui a le plus de capacité libre (capacité mesurée ÷ connexions ouvertes).
- **Surveillance des liens** : toutes les 2 s (branchement / débranchement / changement d'IP) et test internet toutes les 5 s ; un lien qui ne répond plus est ignoré jusqu'à son retour. Ping affiché.
- **Plafond par lien** (Mb/s), mémorisé.
- **Historique** : données par lien et par jour (30 jours affichés, un an conservé).
- **Réglages Windows** en un clic (administrateur) : garder le Wi‑Fi avec le partage USB, lien préféré par Windows, relancer Steam.

Données et réglages : `%APPDATA%\dualnet` (`history.json`, `settings.json`, `dualnet.log`).

## Ligne de commande

```powershell
.\dualnet.exe ui -sticky 0 -transparent     # interface + mode transparent (PowerShell administrateur)
.\dualnet.exe ui                            # interface + proxy, sans droits admin
.\dualnet.exe get https://exemple.com/gros-fichier.iso
.\dualnet.exe proxy -listen 127.0.0.1:1080 -system-proxy
.\dualnet.exe interfaces
```

`-sticky 10m` (par défaut) garde un même site sur le même lien 10 min (utile pour les sites qui surveillent l'IP) ; `-sticky 0` répartit chaque connexion (idéal pour Steam).

## Le Wi‑Fi se coupe quand le téléphone est branché ?

Windows coupe par défaut le Wi‑Fi quand une connexion filaire (le partage USB) est présente. Bouton « Garder le Wi‑Fi avec le partage USB » dans l'interface, ou en PowerShell administrateur :

```powershell
New-Item -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy' -Force | Out-Null
Set-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WcmSvc\GroupPolicy' -Name fMinimizeConnections -Value 0 -Type DWord
```

## Mode transparent : détails

- Carte virtuelle `dualnet` (Wintun) + routes `0.0.0.0/1` et `128.0.0.0/1` ; vos routes normales ne sont pas modifiées.
- Les connexions sont terminées par tun2socks (pile gVisor) et rouvertes sur un lien (TCP et UDP, donc DNS et jeux).
- **IPv6 refusé** : les applications basculent en IPv4. Relancez Steam après l'activation (bouton dans l'interface) : ses connexions déjà ouvertes restent sur leur ancien chemin.
- À la fermeture, même en cas de plantage, la carte et ses routes disparaissent.
- `wintun.dll` 0.14.1 (signée par WireGuard LLC, licence : `internal/transparent/WINTUN-LICENSE.txt`) est intégrée à l'exécutable et extraite à côté au premier lancement.

## Développement

```powershell
go test ./...
go build -o dualnet.exe .
go run ./tools/mkicon     # régénère l'icône (assets/) et la ressource du .exe
```

GitHub Actions compile et teste à chaque push ; un tag `v*` publie `dualnet.exe` dans une release.

## Limites

- Sans serveur intermédiaire, **une seule connexion** (une vidéo, une partie en ligne) ne peut pas utiliser deux liens à la fois : le gain vient des applications qui ouvrent plusieurs connexions (Steam, téléchargements, navigation).
- IPv6 non utilisé en mode transparent.
