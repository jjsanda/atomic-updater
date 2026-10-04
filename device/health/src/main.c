/* main.c — slot determination, configuration, and the mark-good /
 * mark-bad / reboot actions (design.md §7.4).
 *
 * Exit codes: 0 healthy (and confirmed, unless --dry-run), 1 unhealthy or
 * confirmation failed, 2 usage / slot could not be determined.
 */
#define _POSIX_C_SOURCE 200809L

#include "health.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *env_or(const char *name, const char *dflt)
{
    const char *v = getenv(name);
    return v != NULL ? v : dflt;
}

/* Run "rauc status <verb> [extra args]"; returns child exit code. */
static int rauc_mark(const struct ah_config *cfg, const char *verb)
{
    char *extra = ah_strdup(cfg->rauc_extra);
    char *argv[AH_MAX_ARGS];
    int n = 0;
    argv[n++] = "rauc";
    argv[n++] = "status";
    argv[n++] = (char *)verb;
    ah_split(extra, &argv[n], AH_MAX_ARGS - n); /* NULL-terminates */
    int rc = run_argv(argv);
    free(extra);
    return rc;
}

static int act_healthy(const struct ah_config *cfg)
{
    char var[48];
    snprintf(var, sizeof(var), "BOOT_%s_LEFT", cfg->slot);

    if (cfg->dry_run) {
        ah_log("dry-run: would run: rauc status mark-good%s%s",
               cfg->rauc_extra[0] != '\0' ? " " : "", cfg->rauc_extra);
        ah_log("dry-run: would run: fw_setenv %s 3", var);
        return 0;
    }
    int rc = rauc_mark(cfg, "mark-good");
    if (rc != 0) {
        ah_err("rauc status mark-good failed (exit %d); "
               "slot %s stays unconfirmed, next boot retries",
               rc, cfg->slot);
        return 1;
    }
    char *argv[] = { "fw_setenv", var, "3", NULL };
    rc = run_argv(argv);
    if (rc != 0) {
        ah_err("fw_setenv %s 3 failed (exit %d); "
               "slot %s stays unconfirmed, next boot retries",
               var, rc, cfg->slot);
        return 1;
    }
    ah_log("slot %s confirmed: marked good, %s=3", cfg->slot, var);
    return 0;
}

static int act_unhealthy(const struct ah_config *cfg, const char *reason)
{
    ah_err("slot %s unhealthy: %s", cfg->slot, reason);

    if (cfg->dry_run) {
        ah_log("dry-run: would run: rauc status mark-bad%s%s",
               cfg->rauc_extra[0] != '\0' ? " " : "", cfg->rauc_extra);
        ah_log("dry-run: would run: logger -t atomic-health "
               "\"slot %s failed health: %s\"", cfg->slot, reason);
        ah_log("dry-run: would run reboot command: %s", cfg->reboot_cmd);
        return 1;
    }
    int rc = rauc_mark(cfg, "mark-bad");
    if (rc != 0)
        ah_err("rauc status mark-bad failed (exit %d), continuing", rc);

    char msg[AH_REASON_MAX + 64];
    snprintf(msg, sizeof(msg), "slot %s failed health: %s", cfg->slot,
             reason);
    char *largv[] = { "logger", "-t", "atomic-health", msg, NULL };
    if (run_argv(largv) != 0)
        ah_err("logger invocation failed, continuing");

    ah_log("rebooting via: %s", cfg->reboot_cmd);
    rc = run_shell(cfg->reboot_cmd);
    ah_err("reboot command returned (exit %d)", rc);
    return 1;
}

/* Slot: --slot=X flag, else $ATOMIC_SLOT, else rauc.slot= on the kernel
 * command line.  Returns false if none is present. */
static bool determine_slot(struct ah_config *cfg, const char *slot_flag)
{
    if (slot_flag != NULL && slot_flag[0] != '\0') {
        snprintf(cfg->slot, sizeof(cfg->slot), "%s", slot_flag);
        return true;
    }
    const char *env_slot = getenv("ATOMIC_SLOT");
    if (env_slot != NULL && env_slot[0] != '\0') {
        snprintf(cfg->slot, sizeof(cfg->slot), "%s", env_slot);
        return true;
    }
    return ah_parse_cmdline_slot("/proc/cmdline", cfg->slot,
                                 sizeof(cfg->slot));
}

int main(int argc, char **argv)
{
    struct ah_config cfg;
    memset(&cfg, 0, sizeof(cfg));
    const char *slot_flag = NULL;

    for (int i = 1; i < argc; i++) {
        if (strncmp(argv[i], "--slot=", 7) == 0) {
            slot_flag = argv[i] + 7;
        } else if (strcmp(argv[i], "--dry-run") == 0) {
            cfg.dry_run = true;
        } else {
            fprintf(stderr,
                    "atomic-health: unknown argument '%s'\n"
                    "usage: atomic-health [--slot=X] [--dry-run]\n",
                    argv[i]);
            return 2;
        }
    }

    if (!determine_slot(&cfg, slot_flag)) {
        ah_err("cannot determine slot: no --slot flag, no ATOMIC_SLOT, "
               "no rauc.slot= in /proc/cmdline");
        return 2;
    }

    cfg.app_unit   = env_or("ATOMIC_APP_UNIT", "app.service");
    cfg.health_dir = env_or("ATOMIC_HEALTH_DIR",
                            "/usr/lib/atomic-updater/health.d");
    cfg.reboot_cmd = env_or("ATOMIC_REBOOT_CMD", "systemctl reboot");
    cfg.rauc_extra = env_or("ATOMIC_RAUC_EXTRA_ARGS", "");

    const char *timeout_str = env_or("ATOMIC_PROBE_TIMEOUT", "30");
    cfg.probe_timeout = atoi(timeout_str);
    if (cfg.probe_timeout <= 0) {
        ah_err("invalid ATOMIC_PROBE_TIMEOUT '%s', using 30", timeout_str);
        cfg.probe_timeout = 30;
    }

    ah_log("health check starting for slot %s%s", cfg.slot,
           cfg.dry_run ? " (dry-run)" : "");

    char reason[AH_REASON_MAX] = "";
    bool healthy = check_app_unit(&cfg, reason, sizeof(reason)) &&
                   run_probes(&cfg, reason, sizeof(reason));

    if (healthy) {
        ah_log("health result: PASS (slot %s)", cfg.slot);
        return act_healthy(&cfg);
    }
    ah_log("health result: FAIL (slot %s: %s)", cfg.slot, reason);
    return act_unhealthy(&cfg, reason);
}
