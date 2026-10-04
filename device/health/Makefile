# atomic-health — post-boot health check service (design.md §7.4)

CC     := cc
CFLAGS := -std=c11 -Wall -Wextra -Werror -O2
SRC    := $(wildcard src/*.c)
BIN    := atomic-health

$(BIN): $(SRC) src/health.h
	$(CC) $(CFLAGS) -o $@ $(SRC)

.PHONY: test clean

test: $(BIN)
	tests/run.sh

clean:
	rm -f $(BIN)
