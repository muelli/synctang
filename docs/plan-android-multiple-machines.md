<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# Plan: the app with more than one machine, and two ways in

Kept out of the repository, like the other plans. Written 2026-11-11 against `main` at 98169d0
(release v389). `[V]` marks what was read in the code or run, `[I]` what is inferred, `[U]` what is not known.

## What is there now `[V]`

- One machine. `MachineStore` (`UnlockFlow.kt:170`) has `machine()` and `saveMachine()`, and `PairingStore`
  keeps the three fields of it (`machine-device-id`, `-name`, `-discovery`) in `EncryptedSharedPreferences`.
  The encryption is for integrity as much as secrecy: a rewritten Device ID would redirect an unlock at somebody
  else's machine (`Pairing.kt`, class comment).
- `UnlockFlow` is built on that: `connect()` and `approve()` begin with `store.machine() ?: return`
  (lines 825, 893), `NeedsPairing` carries a `previous` machine, a new pairing replaces the old one and offers
  "undo" (`replaced`, `undoReplace`), the readiness chip follows the one machine on the Ready screen
  (`followReadiness`), and a retired key drops the machine (`startNewIdentity`).
- The identity (transport seed) and the key are the phone's, not the machine's: one of each, enrolled on
  every machine the phone pairs with. Nothing about that has to change.
- `MainActivity` is the only activity, `exported` for the launcher. Its comment says the widget, the quick
  settings tile and a second APK per machine were left out on purpose, because every entry point is one more
  way to reach a key that unlocks a disk. That comment has to be rewritten when the entry points go in, and the
  design below is made to keep its point.
- minSdk 31 `[V]` (`build.gradle.kts`), so everything in `RemoteViews` and `ShortcutManager` up to Android 12
  can be used without version checks. `androidx.core:core-ktx:1.12.0` is already a dependency and has
  `ShortcutManagerCompat`.

## Part 1: more than one machine

### Decisions to make (my recommendation first)

1. **What a second pairing does.** Today it replaces the machine, with an undo. With several machines it adds one;
   pairing again with the same Device ID updates its name and discovery server. The "replaced" state and its undo go.
   Removing a machine becomes an explicit "Forget" with a confirmation and an undo snackbar.
2. **The home screen.** With one machine, nothing changes: the Ready screen as it is. With two or more, a list: one
   row per machine with its name, the shortened Device ID, the readiness chip and an Unlock button. A tap on the
   button is the same one-tap unlock as now, for that row. "Pair another machine" is a button under the list.
   No dependence on a setting, so the single-machine case is not touched by the feature.
3. **Names.** A machine's name is what it called itself and is not authenticated (`Pairing.kt`). Two machines can
   share one, or one can pose as another. Rows therefore always show the first group of the Device ID beside the
   name. A local nickname is possible later and not part of this.
4. **A retired key.** Every machine has the old key enrolled, so every machine has to be paired again. Keep the list,
   mark each entry "pair again", and let a scan of that machine's QR code repair it; the alternative (drop them all,
   as one is dropped now) is less code and loses the names. Recommended: keep, because five machines paired again by
   name is a smaller chore than five found again by hand.
5. **Migration from the one-machine store.** None, as decided for everything else: the first start after the update
   has no machines and the phone is paired again. (Reading the three old keys once would be six lines; say so if
   that changes.)

### Storage

- `MachineStore` becomes `machines(): List<Machine>`, `machine(deviceId): Machine?`, `saveMachine(m)`
  (insert or update by Device ID), `forget(deviceId)`, plus `lastUsed(): String?` / `markUsed(deviceId)` to order the list
  and pick what the Ready screen shows. The old `machine()` stays as "the last used one" until the flow is moved over.
- `PairingStore` keeps one JSON array under one key in the same encrypted file, written with `commit()` so that a
  crash does not leave half a list. Only the fields of `Machine` go in; no readiness, no secrets.
- The in-test `FakeStore` becomes a list too.

### The flow

- `connect(machine)` and `approve()` take the machine they act on instead of reading the store; the state already carries
  it (`Connecting`, `Approve`, `Working`, `Done` all hold a `Machine`), so most of this is deleting `store.machine() ?: return`.
- New state `Machines(list, identity, readiness: Map<deviceId, Readiness>)` for two or more; `Ready` stays for one.
- Readiness: today one lookup a minute with jitter, one at a time, a 429 honoured per discovery server. With N machines
  the same rule per machine would be N lookups a minute. Plan: still one lookup at a time, each machine at most once a
  minute, and no more than three a minute in total (so with more than three, each row is older), only while the list is
  on screen and the app is in the foreground. The per-server 429 back-off already in place stays. `[I]` this is far
  below anything the public discovery servers object to; not measured.
- `lastReadiness` is already a map by Device ID, so a failed dial quoting what discovery last knew keeps working.

### The screens

- `ReadyScreen` stays. A new `MachinesScreen` with the rows. Pairing (`PairingScreen`) loses its "replace" wording.
- Forget: long press on a row, or a row's menu, then a dialog naming the machine and its short id, then an undo snackbar
  for the length of the existing snackbar. Forgetting only removes the phone's entry; it does not revoke the key on the
  machine, and the dialog says that and names the command (`synctang-unlocker revoke`). That sentence is a string resource
  like every other, so it is translatable.
- All of it keyboard-navigable and labelled for a screen reader like the rest (`KeyboardNavigation.kt`, `Shortcuts.kt`).

### Tests

- JVM: the flow with a store of one, two and zero machines; pairing adds; pairing the same id updates; forget and undo;
  a retired key marks every entry; the readiness schedule with three machines (fake clock).
- Instrumented Compose test for the list and for forget; `ScreenshotsTest` gets a two-machine screenshot only if the
  listing wants one.

### Order

A. Store and flow take a list; the UI still shows one machine. All existing tests green. One commit per step.
B. The list screen, adding, forgetting, readiness for the list.
C. "Pair again" marking after a retired key.

Not part of the plan: sharing one machine between phones (each phone pairs separately), reordering, groups.

## Part 2: the two ways in (to be built now, on top of the one-machine store)

Both carry a machine's Device ID, so they work unchanged when Part 1 arrives.

### The rule that keeps the old comment's point

**No exported component does anything but open the app.** An activity that a launcher starts through a shortcut or that a
widget's own `PendingIntent` starts does not have to be exported (the system starts those on the app's behalf, `[I]` from
the platform documentation, and the first thing to verify on the emulator). So:

- `UnlockEntryActivity`, `exported="false"`, no UI: it reads the machine's Device ID from the intent, checks that it is a
  machine the phone is paired with, hands it to a small in-process holder (`PendingUnlock`), and starts `MainActivity`
  with a plain intent that has no extras. It finishes at once.
- `MainActivity` takes nothing from its own intent, so a crafted intent from another app can open the app and nothing more.
- When `MainActivity` is up and the flow is idle, it takes `PendingUnlock`, if one is set and not older than ten seconds,
  and starts the unlock for that machine: the one-tap flow that exists, which dials and then raises the fingerprint
  prompt naming the machine. No secret leaves the phone without the fingerprint, as for a tap on the button.
- If the flow is not idle (an unlock is under way, a pairing is, the key needs replacing), the entry does not interrupt
  it: the app comes to the front on what it is showing and the pending request is dropped.
- An unknown or forgotten Device ID shows nothing wrong: the app opens at its home screen.

### 1) The resizable widget

- Built with `AppWidgetProvider` and `RemoteViews`, no new dependency. (Glance would bring Compose runtime for the widget and a
  dependency to verify for the F-Droid scanner and for dependency verification; `[I]` not worth it for a button and a label.)
- Resizable: `resizeMode="horizontal|vertical"`, `targetCellWidth`/`targetCellHeight` (API 31) so it is two cells by one when placed,
  `minResizeWidth`/`minResizeHeight` for one cell, a `previewLayout` for the picker, a `description` string.
- Layouts by size with the size-mapped `RemoteViews` (API 31): one cell is the icon alone; wide is the icon, the machine's
  name and an Unlock label; tall or large is the same with the name allowed to wrap to more lines and the Device ID's first group
  under it. The whole widget is one tap target. `contentDescription` is "Unlock <name>".
- It shows no readiness. A widget that updates itself would have to look the machine up in the background, which is network and
  battery use nobody asked for and tells the discovery server when the phone is awake; a stale dot would also be a lie. The tap is
  what asks, and the app then says why if the machine is not there.
- A configuration activity (`android:configure`) picks the machine when the widget is placed; with one machine it does not ask.
  The widget id to Device ID mapping is kept in `PairingStore` (the encrypted file), for the same reason the machine is.
  A forgotten machine leaves the widget saying "Choose a machine" and a tap opens the app at its home screen.
- The `PendingIntent` is `FLAG_IMMUTABLE`, explicit, and aimed at `UnlockEntryActivity` in this package.
- On a locked phone the launcher asks for the screen to be unlocked first; the widget does not change that.

### 2) The shortcut on a long press of the icon

- Dynamic shortcuts through `ShortcutManagerCompat`, one per paired machine, as many as the launcher shows
  (`getMaxShortcutCountPerActivity`, usually four or five), most recently used first. Each is "Unlock <name>", intent to
  `UnlockEntryActivity` with the Device ID.
- Published when the list changes (pairing, forget) and when the app starts; removed when a machine is forgotten (and any
  pinned copy disabled with a message).
- The label is the machine's own name, which is not authenticated, so it is cut to a launcher-sized length and cleaned of
  control characters before it is shown, the same as in the app. Names then appear on the launcher, where anybody
  holding the phone can read them: it is the first place a machine's name leaves the app (see the questions).
- Pinning a shortcut to the home screen from a machine's row is a small addition (`requestPinShortcut`); not in the first cut.

### Tests

- JVM: `PendingUnlock` (set, taken once, expires, dropped when the flow is busy, unknown id); the shortcut list for zero, one,
  many and over the limit machines, names cleaned and cut; the widget's size choice as a pure function.
- Instrumented on the emulator in CI: launching `UnlockEntryActivity` with a machine's id shows the Connecting screen of that
  machine; launching `MainActivity` with the same extras shows the home screen only (the rule above); the shortcut list matches
  the store. The widget is placed with `AppWidgetManager` and its `RemoteViews` inflated at three sizes, and a screenshot of each
  is looked at (`[U]` whether a widget host can be driven headlessly; fall back to inflating the layouts).
- By hand: place the widget, resize it, tap it with the machine at its prompt; long press the icon and pick the shortcut.
  Fingerprint on a real phone `[U]` until then.

### Order

1. `PendingUnlock` and `UnlockEntryActivity`, with the flow taking the machine. Test first.
2. The shortcuts (smallest, and it exercises the entry).
3. The widget.
4. Update the `MainActivity` comment, the README line about the phone, `docs/usage.md` (how to add the widget and the
   shortcut), and the release note.

## Questions for the owner

- Part 1, decision 4: keep entries "pair again" after a retired key, or drop them as now?
- The shortcut label shows the machine's name on the launcher. Is that acceptable, or should it say only "Unlock" for the most recent
  machine and nothing else?
- A tap on the widget starts the dial at once. Fine, or should the widget ask for a second tap?
