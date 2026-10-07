## stampede users create

Add a member with a password (admin)

### Synopsis

Add a member to the organisation. Their first password is read from
STAMPEDE_NEW_PASSWORD or asked without echo; share it with them and ask
them to change it with stampede password.

```
stampede users create <email> [flags]
```

### Examples

```
  stampede users create pat@acme.test --name "Pat Lee" --role editor
```

### Options

```
  -h, --help          help for create
      --json          print JSON for scripting
      --name string   the member's name (required)
      --role string   owner, admin, editor, runner or viewer (default "viewer")
```

### SEE ALSO

* [stampede users](stampede_users.md)	 - List and manage the organisation's members and their roles

