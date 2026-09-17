cmake_minimum_required(VERSION 3.21)
project(ClassicFarmQt LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Qt6 REQUIRED COMPONENTS Core Network WebSockets Widgets)
qt_standard_project_setup()

qt_add_executable(classic_farm WIN32
    src/main.cpp
    src/loginwindow.cpp
    src/farmwindow.cpp
    src/mailboxdialog.cpp
    src/frienddialog.cpp
    src/farmapiclient.cpp
)
target_link_libraries(classic_farm PRIVATE
    Qt6::Core Qt6::Network Qt6::WebSockets Qt6::Widgets)

if (MINGW)
    target_compile_options(classic_farm PRIVATE -finput-charset=UTF-8 -fexec-charset=UTF-8)
endif()
if (MSVC)
    target_compile_options(classic_farm PRIVATE /utf-8)
endif()
