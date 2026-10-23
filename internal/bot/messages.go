package bot

// Reply texts, verbatim from the old bot. Do not change: users see these.

// PM when the item was already looked up and has a title: name, itemID, title, album, artist, link
const pmKnownHit = "Hallo %s,\n\nDu hast bei https://pr0gramm.com/new/%d nach der Musik gefragt.\nJemand hat bereits danach gefragt, daher erhälst du hier nur eine Kopie.\n\nTitel: %s\nAlbum: %s\nArtist: %s\n\nHier ist ein Link: %s\n\nFalsch erkannt? Über den Link kannst du eine Korrektur vorschlagen."

// PM when the item was already looked up without result: name, itemID, link
const pmKnownMiss = "Hallo %s,\n\nDu hast bei https://pr0gramm.com/new/%d nach der Musik gefragt.\nLeider wurden keine Informationen gefunden.\n\nKennst du den Song? Schlag ihn hier vor: %s"

// Comment on a hit: title, artist, album, link, checkedAt
const commentHit = "Es wurden folgende Informationen dazu gefunden:\n%s - %s\nAus dem Album: %s\n\nHier ist ein Link: %s\nZeitpunkt der Überprüfung %s\n\nFalsch erkannt? Über den Link kannst du eine Korrektur vorschlagen."

// Comment on a miss: checkedAt, link
const commentMiss = "Es wurden keine Informationen zu dem Lied gefunden\n\nZeitpunkt der Überprüfung %s\n\nKennst du den Song? Schlag ihn hier vor: %s"

// checkedAtLayout formats the check time in Europe/Berlin.
const checkedAtLayout = "02.01.2006 um 15:04"
