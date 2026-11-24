# Backups

A backup is the panel's database: users and their usage, protocols and their keys, servers, routing, settings,
sales and the bot. Certificates are not in it: a new server gets its own again. With a backup, a lost server costs
only the time to install Dunvex on a new one and restore.

## Automatic backups

**Settings › Backup**. By default Dunvex takes a backup **every 6 hours** (1, 2, 3, 6, 12 or 24, or only by hand) and
keeps the newest **14** on the server (1 to 500), in `<home>/backups`. It also takes one **before every panel update**.
The first scheduled backup waits five minutes after the panel starts.

Each backup is a consistent copy of the database (SQLite `VACUUM INTO`), gzipped:
`dunvex-<server>-<date>-<time>.db.gz`.

## Off the server

A copy that stays on the server is lost with it. Two places take copies:

- **Telegram**: each scheduled backup goes to the bot's admins as a file (on by default once the bot is set up). The
  daily report then no longer attaches one of its own. Telegram takes files up to 50 MB from bots.
- **An S3-compatible bucket**: Cloudflare R2, ArvanCloud, Backblaze B2, MinIO, AWS or any other. Give the endpoint
  (`https://…`), the bucket, the region (`auto` for R2; `us-east-1` when empty), an access key and its secret, and
  optionally a folder. **Check** writes a small test file, so you know the settings work before a backup needs them.
  Dunvex signs its uploads itself (Signature Version 4, checked against AWS's own published example). Use a private
  bucket and a key for this alone, and a lifecycle rule to remove old copies there.

**Back up and send** makes one now and sends it too. A backup that could not reach Telegram or the bucket stays on the
server, and the failure is a `backup` event: an alert in the bot and for webhooks that follow it. The **Recent backups**
list shows where each copy went (server, Telegram, bucket) and why one did not.

## Encryption

With a **passphrase** (8 characters or more), every backup is encrypted (AES-256-GCM with a key made by scrypt) and
named `….db.gz.enc`: the copies on the server, in Telegram and in the bucket. Without the passphrase they do not
open, and a changed file is refused, not half-restored.

The panel keeps the passphrase so scheduled backups can use it. **Keep it somewhere off the server too**: on a new
server, an encrypted backup needs it. Removing the passphrase makes later backups plain again; older encrypted copies
still need the old one.

## Restore

From the list (**Restore** next to a backup), or **Restore from a file** with a backup from Telegram or the bucket
(`.db`, `.db.gz` or `.db.gz.enc`). An encrypted file opens with the saved passphrase, or the panel asks for it.
Dunvex keeps a copy of the current database (`before-restore-…`, the last three are kept), puts the backup in place
and restarts. Everything changed after the backup is gone.

On a **new server**: install Dunvex, sign in, open Settings › Backup › Restore from a file, and pick the backup. Then
point the domain (or the users' links) at the new address: links follow the server's address in Settings.

## API

All owner only.

| Method | Path | |
| --- | --- | --- |
| GET | `/backups` | Settings (secrets left out), the backups on the server, recent runs, the next one |
| PUT | `/backups/settings` | `hours`, `keep`, `telegram`, `passphrase` / `clearPassphrase`, `s3` (`endpoint`, `region`, `bucket`, `prefix`, `access`, `secret`) / `clearS3` |
| POST | `/backups` | Make one now; `{"offsite": true}` sends it too |
| POST | `/backups/check-bucket` | Write a test file to a bucket (the saved secret when none is given) |
| GET | `/backups/{name}` | Download one |
| DELETE | `/backups/{name}` | Delete one |
| POST | `/backups/{name}/restore` | Restore it (`{"passphrase": …}` when it is encrypted with another one) |
| POST | `/restore` | Restore an uploaded file (form fields `file`, `passphrase`) |
| GET | `/backup` | A fresh plain `.db`, as before |
