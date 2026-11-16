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

## Token benchmark

gws and a direct call to the Google API reach the same data through the same API calls. The difference is what the agent must write and read. gws keeps the lookup, the checks and the output shape inside the binary, so the agent context holds less.

### Method

1. The test sheet is a real task tab with 28 rows and 9 columns (11 KB).
2. "Raw API" is what an agent sends and reads with `curl`. The REST API returns indented JSON by default.
3. `out` counts the tokens that the agent writes: commands and code. `in` counts the tokens that the agent reads: tool results.
4. "Weighted" is `in + 5 × out`, because output tokens cost about 5 times more than input tokens.
5. The counts come from the `o200k_base` tokenizer. Other tokenizers give other counts, but the ratios stay close.
6. In S3, the dropdown-rule response and the update response are synthetic, with the real API shape. All other outputs are measured.

### Flow: set one cell by key (S3)

```
Raw API: the agent drives each step              gws: one command
├─ write token refresh curl        52 out        gws sheet cell <link> --where "Task ID=T-001"
├─ write tab-title curl            64 out          --col Status --set Done           68 out
│  └─ read sheet properties       134 in         └─ inside the binary, not in context:
├─ write values curl               69 out           ├─ Spreadsheets.Get  gid to tab title
├─ write row-lookup code          126 out           ├─ Values.Get        whole tab
│  └─ read A1 address              15 in            ├─ key match: 0 or 2+ rows, refuse
├─ write dropdown-rule curl        74 out           ├─ dropdown check
│  └─ read dataValidation JSON    187 in            └─ Values.Update     [Sheet write]
└─ write update curl               94 out        → updated 'Task'!C2 (Status, row 2):
   └─ read update response         75 in           "Todo" -> "Done"                 22 in
   = 479 out + 411 in                            = 68 out + 22 in
```

Both sides make the same 4 Google API calls. With the raw API, the agent also writes the URL quoting, the row lookup and the dropdown compare. A missed check writes a wrong row or a value that is not in the dropdown.

### Results

| # | Scenario | Approach | out | in | Total | % of raw | % of raw, weighted |
|---|---|---|---|---|---|---|---|
| 1 | S1: read the whole tab | Raw API (indented JSON) | 185 | 4,329 | 4,514 | 100% | 100% |
| 2 | | gws, TSV (default) | 52 | 3,516 | 3,568 | **79.0%** | **71.9%** |
| 3 | | gws `--csv` | 52 | 3,545 | 3,597 | 79.7% | 72.4% |
| 4 | | gws `--json` (compact) | 52 | 3,657 | 3,709 | 82.2% | 74.6% |
| 5 | S2: read 3 columns of the open rows | Raw API, the agent reads the full JSON | 185 | 4,329 | 4,514 | 100% | 100% |
| 6 | | Raw API, the agent writes filter code | 297 | 796 | 1,093 | 24.2% | 43.4% |
| 7 | | gws, TSV (default) | 70 | 592 | 662 | **14.7%** | **17.9%** |
| 8 | | gws `--csv` | 70 | 587 | 657 | 14.6% | 17.8% |
| 9 | | gws `--records` | 72 | 890 | 962 | 21.3% | 23.8% |
| 10 | S3: set one cell by key | Raw API, the agent writes lookup code | 479 | 411 | 890 | 100% | 100% |
| 11 | | gws `cell --set` | 68 | 22 | 90 | **10.1%** | **12.9%** |

### What the numbers show

1. Column and row picks save the most. S2 drops from 4,514 to 662 tokens, 85% less. The format choice adds little to that.
2. Logic in the binary saves the expensive tokens. In S3, most of the raw cost is code that the agent writes. gws replaces it with one command.
3. On this tab, TSV, CSV and compact JSON differ by less than 4%. TSV stays the default because it needs no quoting and one row is always one line.
4. `--records` costs about 1.5 times TSV, because each row repeats its keys. Use it when you need `_row` or named fields.
5. The table does not count retries, extra agent turns, or tool results that the agent reads again on later turns. Each of these adds more to the raw API side.

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

### 4b. A cell that holds a tab, a new line or a backslash

```bash
gws sheet get https://docs.google.com/spreadsheets/d/<SHEET_ID>/edit --cols "Task ID,Notes" --limit 3
```

Output (a real tab separates the columns):

```
Task ID	Notes
T-001	step 1\tstep 2
T-002	line one\nline two
T-003	path C:\\tmp, "quoted"
```

The T-001 cell holds a real tab, and gws prints it as `\t`. The T-002 cell holds a new line, printed as `\n`. A backslash is doubled. A comma or a quote stays as it is. So a row keeps its column count and stays on one line. Use `--json` to get the exact cell text.

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
