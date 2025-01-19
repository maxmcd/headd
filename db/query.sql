/* name: GetHost :one */
SELECT * FROM hosts
WHERE id = ? LIMIT 1;


/* name: InsertHost :one */
INSERT INTO hosts (user, hostname) VALUES (?, ?) RETURNING *;


/* name: InsertApp :one */
INSERT INTO app (name, user, meta) VALUES (?, ?, ?) RETURNING *;
