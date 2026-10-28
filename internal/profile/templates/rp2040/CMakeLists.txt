cmake_minimum_required(VERSION 3.13)

# slepok sets PICO_SDK_PATH to the Pico SDK of the project environment.
include($ENV{PICO_SDK_PATH}/external/pico_sdk_import.cmake)

project(@NAME@ C CXX ASM)
set(CMAKE_C_STANDARD 11)
set(CMAKE_CXX_STANDARD 17)

pico_sdk_init()

add_executable(@NAME@ main.c)
target_link_libraries(@NAME@ pico_stdlib)

# On Pico W the LED is wired to the wireless chip.
if (PICO_CYW43_SUPPORTED)
    target_link_libraries(@NAME@ pico_cyw43_arch_none)
endif()

pico_add_extra_outputs(@NAME@)
