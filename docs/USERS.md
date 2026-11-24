# Tags and filters

Tags are your own labels on users: `vip`, `family`, `office`, a city, the shop they came from. They help you find
users, act on many at once and send a message to one group. Users carry no tags until you add them, except the free
trials of the Telegram bot, which get the tag `trial`.

## Adding tags

- **One user**: open the user, press **Edit**, and type in **Tags**. Enter or a comma (`,` or `،`) adds the tag;
  Backspace in an empty field removes the last one. The tags already in use are offered under the field.
- **New users**: the same field is in **New user**; when you create many users at once, they all get the tags.
- **Many users**: select them on the **Users** page and press **Tag**. Choose **Add** or **Remove**, type the tag or
  pick one in use, and confirm. Users who already have it (or no longer have it) are left as they are.

A tag is up to 24 characters; spaces inside it are kept, and a user carries at most 12 tags. Case does not matter:
`VIP` and `vip` are one tag, written the way it was first used.

Tags are labels only: adding or removing one changes nothing for the user's connections, and no core is reloaded.

## Filters

Under the status tabs on the **Users** page:

| Filter | Users |
| --- | --- |
| Running out | still running, with 7 days or less, or 20% of their data or less, left |
| Never connected | users who have not connected once |
| Unlimited data | users with no data limit |
| A tag | users with that tag; pick several and a user must carry all of them |
| Owner | owner and admins: the panel's own users, or one reseller's |

Filters add up with the status tab and the search (which also looks in tags). The bar shows how many users match and
**Clear filters** puts it back. A tag under a user's name is a shortcut: press it to filter by it. On a phone the
filters fold behind the **Filters** button.

**Select all** then picks only the users shown, so a filter followed by a bulk action works on exactly that group:
extend the users running out, reset those never connected, or message one tag.

## Messages to a tag

In **Broadcast**, **By tag** sends the message to the users with one tag. Each tag shows how many of its users have
linked Telegram (they get the message). See [BROADCAST.md](BROADCAST.md).

## Resellers

A reseller sees, filters and tags only their own users, and the tag list shows only the tags on their users.

## API

| Method | Path | Role | |
| --- | --- | --- | --- |
| GET | `/users?tag=vip&tag=iran` | R | Users with every tag asked for (case does not matter) |
| GET | `/users/tags` | R | The tags in use and how many users carry each, the most used first: `[{"tag":"vip","count":12}]` |
| POST / PUT | `/users`, `/users/{id}` | R | `"tags": ["vip", "family"]` sets a user's tags (the list replaces the old one) |
| POST | `/users/bulk` | R | `{"ids":[1,2],"action":"tag","tag":"vip"}` adds a tag; `"untag"` removes it. Answers `{"affected": N}` |
