# Makefile for Kernel Module Stress Test

obj-m += stress_module.o

KDIR := /lib/modules/$(shell uname -r)/build
PWD := $(shell pwd)

all:
	$(MAKE) -C $(KDIR) M=$(PWD) modules

clean:
	$(MAKE) -C $(KDIR) M=$(PWD) clean

install: all
	sudo insmod stress_module.ko

uninstall:
	sudo rmmod stress_module

.PHONY: all clean install uninstall
