# gws

gws is a bridge between a terminal, or an AI agent, and Google Workspace: Drive, Docs and Sheets. It is one command-line binary. Every command works on one item, and you name that item by its link. You do not copy IDs. You paste the link from the browser address bar.

## Why gws

1. Link in, no IDs to copy. Paste the browser link and the command does the rest.
2. One binary. Install it once with `go build`. It has no runtime to set up.
3. The credential lives in the OS keyring. gws never writes it to a file. `gws auth status` never prints a secret.
4. Safe writes. `sheet cell --where` stops when the key matches zero rows or more than one row. `sheet cell --set` checks the dropdown rule before it writes.
5. Small blast radius. gws has no list, search, delete or share command for Drive. It reaches one item at a time, and only the items shared with your account.
6. Office files and native files are handled apart. `file convert` copies an Office file into a native Google Doc, Sheet or Slides file. It does not change the original.

## Built for AI agents

gws keeps its output small, so an agent spends fewer tokens and makes fewer round trips.

1. Table output is TSV by default. TSV has no quotes and no padding, so it costs the fewest tokens. A tab, a new line or a backslash in a cell is written as `\t`, `\n` or `\\`, so one row stays on one line. On a terminal, the tables are aligned for a person.
2. `--json` is optional and prints compact JSON. Add `--pretty` for an indented copy that people can read. `sheet get --csv` prints CSV.
3. `--cols`, `--where`, `--limit` and `--records` return only the columns and rows that the agent asks for.
4. `--records` and `--json` with a pick flag leave out empty cells. A hand-kept sheet holds many empty cells.
5. Each record holds `_row`, the sheet row number. Keep it to confirm the row before a write.
6. `sheet find` and `comment` cut a long cell to 60 characters. The cut happens only on a terminal, and `--full` turns it off.

## Install

Requires Go 1.26.0, the version on the `go` line of `go.mod`.

```bash
git clone https://github.com/iqsamwahyu/gws-cli
cd gws-cli
go build -o ~/.local/bin/gws .
```

## Log in

gws has two login modes. Pick one.

1. Service account. Pass the key file. Then share each Doc, Sheet or folder with the service account email as Editor. gws reaches only the items that you share with it.
   ```bash
   gws auth login --service-account /path/key.json
   gws auth login --service-account /path/key.json --impersonate user@example.com
   ```
   `--impersonate` uses domain-wide delegation. It works only with a service account key.
2. OAuth desktop client. Run `gws auth login --oauth`. gws asks for the OAuth client ID and client secret. The secret input is hidden. gws opens the consent page in the browser. The redirect comes back to a local loopback address, with PKCE.
   ```bash
   gws auth login --oauth
   ```

Both modes store the credential in the OS keyring. If the keyring is not available, gws refuses to store the credential as plain text.

Check the login:

```bash
gws auth status
```

Output (illustrative):

```
Account: user@example.com
Mode:    oauth
Store:   OS keyring
```

`Mode` is `oauth` or `service_account`. `auth status` prints `Impersonate:` too when one is set.

## Usage

Each command takes a link as its first argument. A bare file ID also works for most commands.

| Area | Command | What it does |
|---|---|---|
| auth | `auth login` | Log in with a service account key or an OAuth desktop client |
| auth | `auth status` | Show the account and the login mode |
| auth | `auth logout` | Remove the credential from the OS keyring |
| doc | `doc get` | Print the body text of a Doc, or its raw JSON with `--json` |
| doc | `doc new` | Make a Doc. `--from` fills it. `--parent` picks the folder |
| doc | `doc append` | Add text to the end of a Doc |
| doc | `doc insert` | Insert text at a UTF-16 index |
| doc | `doc replace` | Replace every match of a substring. `--match-case` is optional |
| sheet | `sheet tabs` | List the tabs of a spreadsheet |
| sheet | `sheet cols` | List the header row with the column letter and index |
| sheet | `sheet get` | Read a range as TSV (`--csv`, `--json` optional). `--cols`, `--where`, `--limit`, `--records` pick rows |
| sheet | `sheet find` | Find rows that match a condition and print their row numbers |
| sheet | `sheet tally` | Count the rows for each distinct value of one column |
| sheet | `sheet cell` | Read one cell by key column, or write it with `--set` |
| sheet | `sheet note` | Read or write the note on one cell |
| sheet | `sheet set` | Overwrite a range with CSV values |
| sheet | `sheet append` | Append CSV rows after a range |
| sheet | `sheet clear` | Clear the values in a range |
| sheet | `sheet addtab` | Add a tab |
| sheet | `sheet new` | Make a Sheet. `--csv` fills the first tab |
| sheet | `sheet dropdown` | List, set, copy or clear the dropdown rule of a column |
| file | `file meta` | Show the Drive metadata of a file |
| file | `file get` | Download a file, or export a Google-native file, to a local path |
| file | `file put` | Replace the content of a Drive file from a local file |
| file | `file new` | Upload a local file as a new Drive file |
| file | `file move` | Move an item into a folder |
| file | `file convert` | Copy an Office file into a native Doc, Sheet or Slides file |
| folder | `folder new` | Make a Drive folder |
| comment | `comment list` | List the open comment threads. `--all` adds the resolved ones |
| comment | `comment add` | Start a comment thread. `--mention` adds an email |
| comment | `comment reply` | Reply inside a thread |
| comment | `comment resolve` | Resolve a thread |
| people | `people` | List who can reach the item, with email addresses |
| open | `open` | Show what a link points to: id, name, kind and owners |

## Examples

These outputs are illustrative. The field names and formats follow the code. The values are placeholders.

### 1. Open a link

```bash
gws open https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit
```

Output:

```
FIELD          VALUE
id             <SHEET_ID>
name           Task list
kind           sheet
mimeType       application/vnd.google-apps.spreadsheet
modifiedTime   2026-11-10T08:30:00.000Z
size           -
owners         user@example.com
webViewLink    https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit
driveId        -

Handled by: gws sheet
```

### 2. List the columns

```bash
gws sheet cols https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit
```

Output:

```
LETTER  INDEX  HEADER
A       1      Task ID
B       2      Task name
C       3      Status
D       4      Priority
```

### 3. Read rows as TSV

```bash
gws sheet get https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit --cols "Task ID,Status" --where Status=Todo
```

Output:

```
Task ID	Status
T-001	Todo
T-004	Todo
```

### 4. Read the same rows as records

```bash
gws sheet get https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit --cols "Task ID,Status" --where Status=Todo --records
```

Output:

```
[{"_row":2,"Task ID":"T-001","Status":"Todo"},{"_row":5,"Task ID":"T-004","Status":"Todo"}]
```

The output is one JSON array. Each element is one row. Add `--pretty` to indent it. An empty cell does not appear in its object.

### 5. Write one cell

```bash
gws sheet cell https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit --where "Task ID=T-001" --col Status --set Done
```

Output:

```
updated 'Tasks'!C2 (Status, row 2): "Todo" -> "Done"
```

gws stops with an error when no row or more than one row matches `Task ID=T-001`. It also stops when the Status dropdown does not list `Done`. Pass `--force` to write the value anyway.

### 6. Append text to a Doc

```bash
gws doc append https://docs.google.com/document/d/<DOC_ID>/edit "Sprint note: T-001 is done."
```

Output:

```
appended to <DOC_ID>
```

Quote any argument that holds a space or a comma.
