/* atomic-health — post-boot health check service (design.md §7.4).
 *
 * Determines the booted slot, verifies the application unit and the
 * user-defined probes under the health.d directory, then confirms the slot
 * (rauc status mark-good + fw_setenv) or rejects it (rauc status mark-bad +
 * logger + reboot).  C11, POSIX only.
 */
#ifndef ATOMIC_HEALTH_H
#define ATOMIC_HEALTH_H

#include <stdbool.h>
#include <stddef.h>

#define AH_MAX_ARGS   32
#define AH_REASON_MAX 512

/* run_probe() sentinel results (negative: cannot collide with exit codes) */
#define AH_PROBE_TIMEOUT (-2)
#define AH_PROBE_ERROR   (-1)

struct ah_config {
    char        slot[32];      /* "A" / "B" */
    const char *app_unit;      /* NULL or "" disables the app-unit check */
    const char *health_dir;    /* directory scanned for *.sh probes */
    int         probe_timeout; /* seconds, per probe */
    const char *reboot_cmd;    /* run via sh -c on unhealthy */
    const char *rauc_extra;    /* extra whitespace-separated rauc args */
    bool        dry_run;       /* report only, no side effects */
};

/* util.c */
void  ah_log(const char *fmt, ...); /* info line to stdout */
void  ah_err(const char *fmt, ...); /* error line to stderr */
char *ah_strdup(const char *s);     /* strdup or die */
/* Split s in place on whitespace into argv (NULL-terminated); returns count. */
int   ah_split(char *s, char **argv, int max);
/* Extract the value of "rauc.slot=" from a kernel cmdline file. */
bool  ah_parse_cmdline_slot(const char *path, char *out, size_t outsz);

/* exec.c — every child is spawned via fork+execvp so PATH resolves fakes. */
int run_argv(char *const argv[]);    /* exit code, 128+sig, or -1 */
int run_shell(const char *cmd);      /* sh -c <cmd> */
int run_probe(const char *path, int timeout_s); /* own pgid + timeout kill */

/* probes.c — checks write a human-readable failure reason on false. */
bool check_app_unit(const struct ah_config *cfg, char *reason, size_t rsz);
bool run_probes(const struct ah_config *cfg, char *reason, size_t rsz);

#endif /* ATOMIC_HEALTH_H */
